# Arquitetura

Como o plugin `mapex-broker.so` funciona internamente — o que é executado em qual
thread, como os dados fluem entre os callbacks do Mosquitto e o NATS, e quais
invariantes a implementação depende.

## Modelo de processo

O Mosquitto executa como um loop de eventos single-threaded. Quando um plugin é
carregado via `dlopen`, o broker chama `mosquitto_plugin_init()` uma vez,
durante a qual o plugin registra callbacks para eventos específicos. A partir
desse ponto, todo evento é disparado na **broker thread** — a mesma
thread que conduz a máquina de estados do protocolo MQTT.

```
┌─────────────────────────────────────────────────────────────────┐
│ Mosquitto process (single thread for protocol)                  │
│                                                                 │
│   ┌──────────────┐    dlopen()    ┌──────────────────────────┐  │
│   │ broker core  │────────────────▶│ mapex-broker.so          │  │
│   │              │                 │                          │  │
│   │              │ ◀───callbacks── │  init / cleanup          │  │
│   │              │                 │  4 hooks (auth/acl/      │  │
│   │              │                 │  disconnect/message)     │  │
│   └──────────────┘                 │                          │  │
│                                    │  Go runtime + GC         │  │
│                                    │  (cgo c-shared mode)     │  │
│                                    │                          │  │
│                                    │  AsyncPublisher  ◀────┐  │  │
│                                    │  (buffered channel)   │  │  │
│                                    │  + N worker goroutines│  │  │
│                                    └───────────────────────┼──┘  │
└────────────────────────────────────────────────────────────┼─────┘
                                                             │
                                       fire-and-forget       │
                                       NATS publish ◀────────┘
```

Dois contextos de execução coexistem dentro do processo do broker:

1. **Broker thread** — executa cada callback de forma síncrona. Qualquer
   operação lenta aqui paralisa todos os clientes MQTT. Contrato:
   - A verificação de ACL retorna em microssegundos (comparação de strings).
   - O callout de autenticação conclui dentro do timeout configurado (padrão 5s).
   - Os callbacks de connect/disconnect/message publicam em um canal Go e
     retornam imediatamente.

2. **Worker goroutines** — o pool do AsyncPublisher. Elas drenam o
   canal e chamam `nats.Conn.Publish` (Core, fire-and-forget). Se
   o NATS ficar lento, o canal enche, novos eventos são descartados (contabilizados),
   e a broker thread continua em movimento. Disponibilidade acima de consistência.

## Os quatro hooks

O plugin registra os eventos que a plataforma realmente precisa. O Mosquitto
2.0.x não possui `MOSQ_EVT_CONNECT` (adicionado posteriormente no branch master), portanto o
sinal de conexão é derivado da transição auth-success.

### MOSQ_EVT_BASIC_AUTH (síncrono, bloqueia o broker)

Disparado a cada CONNECT com username + password (e opcionalmente um
certificado de cliente no listener TLS). O plugin:

1. Lê `username`, `password`, `clientid` e (quando TLS)
   `certSerial` da struct de evento C, copiando via `C.GoString` para que
   o lado Go seja dono dos bytes após o retorno do callback.
2. Faz o parse do `username` como o `assetUUID` puro (globalmente único).
3. Consulta o `AuthEntry` via **TieredAuthStore** (L1 Pebble →
   L2 MinIO → L3 HTTP fallback). Sem round-trip HTTP no caminho quente.
4. Determina o modo de autenticação a partir do registro:
   - **password mode**: compara o `password` contra
     `entry.PasswordHash` localmente via bcrypt. Um asset em modo cert que apresenta
     password é negado.
   - **cert mode**: compara o serial do certificado do dispositivo contra
     `entry.CurrentCertSerial`. Um asset em modo password que apresenta um
     cert é negado.
5. Mapeia o resultado: Allow → `MOSQ_ERR_SUCCESS`, Deny →
   `MOSQ_ERR_AUTH`, Store error → `MOSQ_ERR_UNKNOWN` (fail-closed).
6. Em Allow, deriva o sinal de conexão: enfileira um advisory de presença
   (`event:"connect"`) no AsyncPublisher e persiste o
   par confiável `(orgId, assetUUID)` no mapa de sessão.

A consulta é executada de forma síncrona na broker thread porque o Mosquitto
bloqueia o handshake do CONNECT aguardando a decisão de autenticação — async é
impossível pelo protocolo. O cache L1 mantém o p99 bem abaixo de 1ms para
CONNECTs no caminho quente.

### MOSQ_EVT_ACL_CHECK (síncrono, em memória)

Disparado a cada PUBLISH e SUBSCRIBE. Comparação pura de strings em Go contra
a estrutura de topic + username:

```
username = "{assetUUID}"   (bare; globally unique)
allowed topics:
  events/{assetUUID}/+    acc=2 (publish)
  commands/{assetUUID}/+  acc=1 (read), acc=4 (subscribe)
```

Os wildcards `+` e `#` no nível de event-type ou command-type são
permitidos quando o slot assetUUID corresponde ao username. Wildcards
cross-asset (`+` no slot assetUUID) são negados. Sem I/O,
sem alocações no caminho quente, latência sub-microssegundo.

### MOSQ_EVT_DISCONNECT (assíncrono, fire-and-forget)

Disparado quando uma conexão é encerrada — limpa, timeout de keepalive, tomada de
sessão, kick administrativo. O plugin faz o parse do username, constrói um
`PresenceAdvisory{event:"disconnect", reasonCode, reasonText}`,
o enfileira e retorna. O healthmonitor downstream aplica um
invariante anti-race (`disconnectAt > lastConnectAt`) para ignorar
duplicatas obsoletas após uma reconexão rápida.

### MOSQ_EVT_MESSAGE (assíncrono, fire-and-forget)

Disparado a cada PUBLISH que passou pelo ACL. O plugin constrói um
`IngressMessage{topic, payload, qos, retain, ts}` e o publica em
um subject por dispositivo:

```
{NATS_SUBJECT_INGRESS_PREFIX}.{orgId}.{assetUUID}
```

O MqttDataConsumer do JS-Executor assina `{prefix}.>` para que as mensagens de
cada dispositivo sejam roteadas diretamente para a cadeia de filtros downstream.

O tamanho do payload é limitado a 900 KiB (o max_payload padrão do NATS é 1 MiB;
o envelope JSON consome um pouco). Payloads maiores são descartados com um log WARN
identificando o asset e o topic — o servidor NATS subjacente
rejeitaria de qualquer forma; isso evidencia o problema mais cedo com contexto completo.

## Publicação NATS

O plugin usa **Core NATS** (não JetStream) para publicações. O Core não
possui ACK do lado do servidor — `nats.Conn.Publish` escreve em um buffer de saída
local e retorna. A goroutine de flush dentro do `nats-go` drena o
buffer para o TCP de forma assíncrona. Isso é fire-and-forget por design:

- Uma única chamada `Publish` custa microssegundos.
- Os streams JetStream downstream da plataforma capturam os subjects;
  a durabilidade acontece no lado do servidor sem envolvimento do produtor.
- Se o NATS ficar brevemente indisponível, o `nats-go` mantém até 16 MiB em buffer
  durante a reconexão (`ReconnectBufSize`); além disso, os publishes retornam
  erros que o worker contabiliza como `publishErr` e registra em log.

Opções de resiliência (`src/connect.go`):

```
MaxReconnects(-1)              never give up
ReconnectWait(1s)              exponential-ish backoff
ReconnectBufSize(16 MiB)       drain on reconnect
NoEcho()                       don't echo our publishes back
FlusherTimeout(50ms)           push small bursts quickly
PingInterval(2m)               detect half-open connections
```

Os lifecycle hooks registram cada desconexão, reconexão e erro assíncrono no
log estruturado `[PLUGIN:Mosquitto]` para que a operação visualize as bordas de
indisponibilidade.

## AsyncPublisher

`src/nats_publisher.go`. Um canal limitado + pool de workers que
isola a broker thread da lentidão do NATS.

```go
type AsyncPublisher struct {
    queue   chan publishJob
    workers int
    closeMu sync.RWMutex
    ...
}

func (p *AsyncPublisher) Enqueue(subject string, data []byte) bool {
    if !p.started.Load()    { return false }   // pre-Start guard
    p.closeMu.RLock(); defer p.closeMu.RUnlock()
    if p.closed             { return false }
    select {
    case p.queue <- job:   p.enqueued++; return true
    default:               p.dropped++;  return false   // overflow
    }
}
```

Invariantes que a implementação garante:

1. **`Enqueue` nunca bloqueia.** `select` não-bloqueante com `default`,
   portanto um canal cheio descarta o job. A broker thread nunca espera
   pelo NATS.

2. **`closeMu` RWMutex serializa o fechamento do canal vs envio.** Uma
   verificação ingênua do flag `closed` + envio concorrendo com `close(channel)` poderia
   causar pânico com "send on closed channel" e crashar o processo do broker.

3. **Recuperação de pânico nos workers.** Um pânico dentro de `nats.Conn.Publish` (nil
   deref durante reconexão, subject malformado) é contido por
   `defer recover()` por job. O worker continua drenando; o contador
   `panics++`. Um evento que cause crash no worker acabaria
   enchendo o canal e degradando silenciosamente.

4. **Drain idempotente.** `Drain()` pode ser chamado mais de uma vez;
   chamadas subsequentes retornam imediatamente. A goroutine interna `wg.Wait()`
   vaza no máximo uma vez por processo sob timeout.

5. **Contadores atômicos.** Todos os cinco (`enqueued`, `published`,
   `publishErr`, `dropped`, `panics`) usam `atomic.Uint64`, seguros para
   leituras de estatísticas concorrentes de qualquer thread.

## Segurança de memória através da fronteira cgo

Toda string C e byte slice é **copiada** da memória pertencente ao broker
antes de chegar às goroutines worker:

```go
username := C.GoString(C.mosquitto_client_username(client))   // copies
payload  := C.GoBytes(ed.payload, C.int(ed.payloadlen))       // copies
```

O Mosquitto libera os buffers de origem quando o callback retorna. Se
mantivéssemos ponteiros com alias, o worker leria memória liberada e o
broker sofreria segfault. Toda extração não trivial passa por
`C.GoString` / `C.GoBytes` por segurança.

## Modos de falha que o plugin trata

| Failure | Behavior |
|---|---|
| NATS unreachable at startup | `mosquitto_plugin_init` returns `MOSQ_ERR_UNKNOWN`; broker fails to start (loud, immediate) |
| NATS dies after startup | `nats-go` reconnects automatically up to ReconnectBufSize. Past buffer, publishes error and counter `publishErr` grows |
| NATS slow | AsyncPublisher channel fills, `dropped++` per overflow. Broker thread never blocks |
| `Enqueue` before `Start` | Rejected, `dropped++` and `droppedNoStart++` (misuse signal). Items would otherwise sit in channel forever |
| Concurrent `Drain` + `Enqueue` | RWMutex serializes; no panic, no race |
| `nats.Publish` panics | `defer recover()` per worker, `panics++`, worker keeps draining |
| All cache layers down | L1 miss + L2 miss + L3 timeout → `AuthError` → broker denies CONNECT (fail-closed) |
| L2 (MinIO) down | L1 miss falls through to L3 (HTTP); slower but functional |
| L3 (Assets MS) down | Only matters when L1 + L2 both miss — rare on warm path |
| Assets MS returns 404 | `AuthDeny` → asset does not exist → broker rejects CONNECT |
| Username with NATS-illegal chars (`.`, `*`, `>`, whitespace) | Ingress publish dropped with WARN log; nothing leaves the plugin |
| Payload > 900 KiB | Dropped with WARN identifying asset + topic + size |
| Plugin compiled against newer Mosquitto than runtime | Plugin v5 API is forward-compatible; events not present at runtime simply never fire |

## Logging

`src/log.go`. Cada linha de log tem o prefixo `[PLUGIN:Mosquitto]`
seguido de um nível (`DEBUG INFO  WARN  ERROR`) para que dashboards de operação possam
filtrar por ambos. O Mosquitto roteia o stderr do plugin para seu próprio destino
de log (`stdout` no container), portanto os coletores veem os eventos do plugin
junto com os eventos do broker.

O nível padrão é `INFO`. As linhas `DEBUG` são emitidas para decisões de
autenticação por CONNECT (amostra para rastreamento) e detalhes por evento — habilitados
explicitamente via override de variável de ambiente (consulte `docs/config.md`).

## Contadores de observabilidade

O estado do lado do plugin é exposto via métodos em `AsyncPublisher` e
`AuthClient`:

| Counter | Meaning |
|---|---|
| `EnqueuedCount` | Total events accepted by AsyncPublisher |
| `PublishedCount` | Total successful NATS publishes |
| `PublishErrorCount` | NATS-side errors (timeout, buffer overrun, panic) |
| `DroppedCount` | Total drops — channel full + pre-Start drops |
| `DroppedNoStartCount` | Drops attributed to misuse (Enqueue before Start) |
| `PanicCount` | Recovered worker panics — should be zero |
| `Auth.AllowedCount` | Auth allowed (password or cert match) |
| `Auth.DeniedCount` | Auth denied (wrong password, wrong cert, disabled asset) |
| `Auth.ErrorCount` | Auth infra failures (all cache layers unavailable) |
| `Auth.RequestCount` | Total auth attempts |

Uma futura passagem de exposição via Prometheus pode conectar esses contadores a um endpoint HTTP
dentro do processo do plugin. Por ora, eles vivem apenas em memória;
inspecione via `pprof` ou `gdb`-attach se necessário.

## Não-funcionalidades deliberadas

O que o plugin **não** faz, intencionalmente:

- **Sem acesso direto ao banco de dados.** O plugin lê o estado de autenticação apenas
  através do TieredAuthStore (L1 Pebble → L2 MinIO → L3 HTTP). O
  assets MS é o único escritor; a coerência é conduzida por
  invalidação FANOUT.
- **Sem tratamento de persistência de mensagens QoS 2.** O Mosquitto lida com
  retransmissões QoS 1+ e assinaturas duráveis via sua própria camada de persistência.
  O plugin apenas observa o evento `MESSAGE` pós-ACL.
- **Sem rastreamento de assinaturas.** `MOSQ_EVT_SUBSCRIBE` existe em versões mais
  recentes do Mosquitto, mas a plataforma deriva as assinaturas do estado do
  dispositivo (asset template), não do estado do broker em tempo de execução.
- **Sem publishes JetStream.** O Core NATS é suficiente; a durabilidade é
  responsabilidade da configuração de streams no lado do consumidor, não do produtor.
