# Configuração

Como o operador configura o container `mapex-broker-mqtt` em
tempo de execução. Todos os parâmetros são expostos como **variáveis de ambiente**.
O plugin `mapex-broker` as lê diretamente pelo fluxo compartilhado
`mapexGoKit/config` — o mesmo mecanismo `InitConfig` / `ConfigDefinition`
usado por todo serviço Go do mapexOS. O `/entrypoint.sh` renderiza apenas
o bloco de listener nativo do mosquitto (`mosquitto.conf.template` via
`envsubst`); as configurações de negócio não são mais escritas no
`mosquitto.conf`.

## O guard de produção (`GO_ENV`)

Toda credencial e a `NATS_URL` (que carrega `user:password` inline) tem um
valor padrão amigável para dev, para que um container básico inicie contra o
stack local. Esses padrões nunca devem chegar à produção. O `InitConfig`
executa o guard de padrões sensíveis compartilhado na inicialização:

- `GO_ENV=dev` (ou ausente): o plugin inicia; uma linha `[SECURITY WARNING]`
  nomeia qualquer chave sensível ainda usando seu padrão de dev.
- `GO_ENV` com qualquer valor não-dev (`staging`, `prod`, …): o plugin
  **se recusa a iniciar** se alguma chave sensível ainda estiver no padrão
  de dev, registrando `[SECURITY]` e saindo com código diferente de zero.

As chaves sensíveis são `NATS_URL`, `INTERNAL_API_KEY`,
`OBJECT_STORE_ACCESS_KEY` e `OBJECT_STORE_SECRET_KEY`. Defina-as com valores
reais em todo deployment não-dev.

## Variáveis principais

Possuem padrões de dev; o entrypoint não falha mais rapidamente quando estão
ausentes (o guard acima é o ponto de aplicação em não-dev).

| Variable | Default | Purpose |
|---|---|---|
| `GO_ENV` | `dev` | Seleciona warn (dev) vs abort (não-dev) para o guard de padrões sensíveis. |
| `INTERNAL_API_KEY` | dev key | Segredo compartilhado no header `X-API-Key` do auth callout. DEVE ser igual a `internal_api_key` no assets MS. Sensível. |
| `NATS_URL` | `nats://service:service_secret@localhost:4222` | URL do servidor NATS onde o plugin publica (credenciais inline). Sensível. |
| `ASSETS_HOST` | `assets` | Hostname do listener interno do assets MS. `AUTH_URL` é derivado como `http://{host}:{port}/internal/asset_auth`. |
| `ASSETS_PORT` | `5002` | Porta do listener interno do assets MS. |

## Variáveis opcionais

Os valores padrão abaixo são aplicados pela lista `ConfigDefinition` do plugin
quando a variável está ausente (as vars nativas do mosquitto são default no
`entrypoint.sh`).

### Object store (TieredCache L2)

| Variable | Default | Purpose |
|---|---|---|
| `OBJECT_STORE_ENDPOINT` | `` (vazio) | Endpoint MinIO/S3 do cache L2 de auth-projection. Vazio desabilita o L2 (plugin usa L1 + L3). |
| `OBJECT_STORE_ACCESS_KEY` | `svc-broker` | Usuário escopado do object store. Sensível — sobrescreva em não-dev. |
| `OBJECT_STORE_SECRET_KEY` | `svc-broker-secret-change-me` | Segredo do usuário escopado. Sensível — sobrescreva em não-dev. |
| `OBJECT_STORE_USE_SSL` | `false` | TLS para o object store. |
| `OBJECT_STORE_AUTH_IS_NEEDED` | `true` | `true` = chaves estáticas; `false` = IAM ambiente (chaves ignoradas). |

O nome do bucket L2 é fixado pelo contrato da plataforma
(`mapex-asset-auth`) e não é configurável pelo operador.

### Listener

| Variable | Default | Purpose |
|---|---|---|
| `MQTT_LISTENER_PORT` | `1883` | TCP port the broker binds. Match the `EXPOSE` directive and the docker-compose port mapping. |
| `MQTT_MAX_CONNECTIONS` | `-1` | `-1` = unlimited. Set a positive integer to cap concurrent clients (mosquitto-side, before the plugin sees CONNECT). |

### NATS subjects

O plugin lê `GO_ENV` apenas para acionar o guard de padrões sensíveis — ele
NÃO adiciona prefixo de ambiente aos subjects. Os operadores ainda passam
nomes de subjects completos com prefixo de ambiente, para que os deployments
de `dev`, `staging` e `prod` possam compartilhar um cluster NATS sem colisões.

| Variable | Default | Purpose |
|---|---|---|
| `NATS_SUBJECT_PRESENCE` | `dev.mapexos.presence.advisory` | Subject for `event:"connect"` and `event:"disconnect"` advisories. The healthmonitor module subscribes here. |
| `NATS_SUBJECT_INGRESS_PREFIX` | `dev.mapexos.mqtt.data` | Leading subject token for device PUBLISH events. The plugin appends `.{orgId}.{assetUUID}` per message — JS-Executor's wildcard consumer at `{prefix}.>` routes to per-device filter chains. |

Deployments em produção sobrescrevem o prefixo, por exemplo:

```yaml
NATS_SUBJECT_PRESENCE: prod.mapexos.presence.advisory
NATS_SUBJECT_INGRESS_PREFIX: prod.mapexos.mqtt.data
```

### Auth (TieredCache L3 fallback)

O plugin do broker não realiza NENHUMA chamada HTTP de autenticação. Toda decisão
de CONNECT (bcrypt para modo password, igualdade de serial para modo cert) é tomada
LOCALMENTE a partir da projeção `AuthEntry` retornada pelo TieredAuthStore do plugin
(L1 Pebble → L2 MinIO → L3 HTTP).

O fallback L3 é um GET somente-leitura contra o endpoint do read-model interno
do assets MS. Ele é executado apenas quando tanto L1 quanto L2 falham — o caminho
quente típico nunca chega até ele.

| Variable | Default | Purpose |
|---|---|---|
| `AUTH_TIMEOUT_SECONDS` | `5` | HTTP timeout per L3 lookup. Mosquitto blocks the CONNECT handshake while the plugin awaits the lookup, so this caps the broker-thread parking time. Lower for fast-failing under degraded assets MS, higher only when assets MS warm path is genuinely slow. |

A URL de lookup L3 é construída a partir de `ASSETS_HOST` + `ASSETS_PORT` e o
caminho base canônico:

```
http://${ASSETS_HOST}:${ASSETS_PORT}/internal/assets
```

O plugin acrescenta `/{assetUUID}` por lookup. O caminho é hard-coded
no template — alterá-lo requer editar
`config/mosquitto.conf.template` e reconstruir a imagem. O
endpoint reside dentro do módulo `assets` do assets MS (`GET
/internal/assets/:assetUUID`), protegido pelo middleware padrão de `X-API-Key`.
A resposta é o envelope padrão do MapexOS encapsulando
um `AssetReadModel`; o plugin extrai `protocol.mqtt.passwordHash`
e `currentCert.serial` e descarta o restante.

### Ajuste fino do publisher assíncrono

O publisher NATS do plugin é um channel limitado + worker pool que
isola a thread do broker de lentidões no NATS. Os valores padrão são
dimensionados para ~1k eventos/seg sustentados sem descartes. Ajuste para volumes maiores.

| Variable | Default | Purpose |
|---|---|---|
| `PLUGIN_WORKER_POOL_SIZE` | `4` | Goroutines draining the publish channel. Each worker handles one publish at a time. Increase if `PublishedCount` grows slower than `EnqueuedCount` under load. |
| `PLUGIN_BUFFER_SIZE` | `10000` | Channel capacity. When full, new events are dropped (counted in `DroppedCount`). Increase to absorb longer NATS hiccups; decrease only if memory budget is tight (each slot holds a small struct + a byte slice copy). |

Um `DroppedCount` crescente é o sinal para o operador de que o pool
está subdimensionado para a taxa de eventos do deployment. Aumente
`PLUGIN_WORKER_POOL_SIZE` (mais publicações NATS concorrentes) ou
`PLUGIN_BUFFER_SIZE` (fila mais profunda) — tipicamente o primeiro em primeiro lugar.

## Configuração mínima completa

O menor deployment que inicia e atende dispositivos:

```bash
docker run --rm \
  -p 1883:1883 \
  -e INTERNAL_API_KEY=$(openssl rand -hex 32) \
  -e NATS_URL=nats://nats:4222 \
  -e ASSETS_HOST=assets \
  -e ASSETS_PORT=5002 \
  --network mapex-net \
  docker.io/thiagoanselmo/mapex-broker-mqtt:dev
```

Todo o restante usa os valores padrão. A sequência de boot imprime a
configuração renderizada e o ciclo de vida de inicialização do plugin no stderr:

```
[ENTRYPOINT] config rendered: listener=1883 nats=nats://nats:4222 assets=assets:5002
[ENTRYPOINT] subjects: presence=dev.mapexos.presence.advisory ingress_prefix=dev.mapexos.mqtt.data
INFO  [PLUGIN:Mosquitto] plugin_init: starting
INFO  [PLUGIN:Mosquitto] NATS connected url=nats://nats:4222 server=nats://nats:4222
INFO  [PLUGIN:Mosquitto] AsyncPublisher started: workers=4 buffer=10000
INFO  [PLUGIN:Mosquitto] PluginRuntime initialized: presence=... ingress_prefix=... auth_url=...
INFO  [PLUGIN:Mosquitto] plugin_init: ready (4 callbacks registered)
```

Se alguma variável obrigatória estiver ausente, o entrypoint encerra antes
de o mosquitto iniciar:

```
/entrypoint.sh: line 26: INTERNAL_API_KEY: INTERNAL_API_KEY is required
```

## Referência de produção (docker-compose)

```yaml
services:
  mapex-broker-mqtt:
    image: docker.io/thiagoanselmo/mapex-broker-mqtt:${MAPEX_BROKER_VERSION:-2026.05.08}
    container_name: mapex-broker-mqtt
    ports:
      - "1883:1883"
    environment:
      INTERNAL_API_KEY: ${INTERNAL_API_KEY:?required}
      NATS_URL: nats://nats-core:4222
      ASSETS_HOST: assets
      ASSETS_PORT: "5002"
      NATS_SUBJECT_PRESENCE: ${GO_ENV:-prod}.mapexos.presence.advisory
      NATS_SUBJECT_INGRESS_PREFIX: ${GO_ENV:-prod}.mapexos.mqtt.data
      AUTH_TIMEOUT_SECONDS: "5"
      PLUGIN_WORKER_POOL_SIZE: "8"
      PLUGIN_BUFFER_SIZE: "20000"
    volumes:
      - mosquitto-data:/mosquitto/data
    depends_on:
      nats-core:
        condition: service_healthy
      assets:
        condition: service_started
    restart: unless-stopped
    healthcheck:
      test: ["CMD-SHELL", "nc -z 127.0.0.1 1883 || exit 1"]
      interval: 15s
      timeout: 5s
      retries: 3
      start_period: 10s

volumes:
  mosquitto-data:
```

## Listener TLS (porta 8883)

Deployments voltados para a internet pública e qualquer dispositivo em redes
celular/NB-IoT DEVEM usar TLS. O container inclui um listener TLS que o
entrypoint acrescenta à configuração renderizada quando `TLS_ENABLED=true`.

### Variáveis de ambiente TLS

| Variable | Default | Purpose |
|---|---|---|
| `TLS_ENABLED` | `false` | Set `true` to enable the TLS listener on `MQTT_TLS_LISTENER_PORT`. The plaintext listener on 1883 stays active in parallel — operators control exposure via docker-compose port mappings. |
| `MQTT_TLS_LISTENER_PORT` | `8883` | TCP port for the TLS listener. Match the port mapping in your compose file. |
| `TLS_CERT_FILE` | `/mosquitto/certs/server.crt` | Server certificate (PEM). Container exits at startup if `TLS_ENABLED=true` and this file is missing. |
| `TLS_KEY_FILE` | `/mosquitto/certs/server.key` | Server private key (PEM). Same fail-fast as `TLS_CERT_FILE`. |
| `TLS_CA_FILE` | `` (empty) | Optional CA certificate. When set, mTLS is enabled — clients MUST present a certificate chained to this CA. Empty disables mTLS (server-side TLS only, like HTTPS without client certs). |
| `TLS_REQUIRE_CLIENT_CERT` | `false` | Only meaningful with `TLS_CA_FILE`. When `true`, mosquitto rejects clients that do not present a valid client cert; when `false`, clients may connect with or without a cert. |
| `TLS_MIN_VERSION` | `tlsv1.2` | Minimum TLS version. `tlsv1.2` or `tlsv1.3`. |

### Montagem de certificado (TLS somente servidor)

O caso mais simples — TLS para segurança de transporte, sem certificados de cliente.
Monte seu cert + key em `/mosquitto/certs/` e ative a opção:

```yaml
services:
  mapex-broker-mqtt:
    image: docker.io/thiagoanselmo/mapex-broker-mqtt:dev
    ports:
      - "1883:1883"        # plaintext (internal/dev only)
      - "8883:8883"        # TLS (public devices)
    environment:
      INTERNAL_API_KEY: ${INTERNAL_API_KEY:?required}
      NATS_URL: nats://nats:4222
      ASSETS_HOST: assets
      ASSETS_PORT: "5002"
      TLS_ENABLED: "true"
      TLS_CERT_FILE: /mosquitto/certs/server.crt
      TLS_KEY_FILE:  /mosquitto/certs/server.key
    volumes:
      - ./certs:/mosquitto/certs:ro
```

### mTLS (TLS mútuo)

Para deployments de alta confiança onde cada dispositivo possui um
certificado de cliente emitido pela plataforma. Adicione o arquivo CA e exija-o:

```yaml
environment:
  TLS_ENABLED: "true"
  TLS_CERT_FILE: /mosquitto/certs/server.crt
  TLS_KEY_FILE:  /mosquitto/certs/server.key
  TLS_CA_FILE:   /mosquitto/certs/ca.crt
  TLS_REQUIRE_CLIENT_CERT: "true"
volumes:
  - ./certs:/mosquitto/certs:ro
```

O plugin impõe exclusão mútua entre os modos de autenticação — um
asset no modo password que apresenta um certificado é rejeitado, e vice-versa.
O `use_identity_as_username false` é hard-coded, de forma que o campo de username
no CONNECT é sempre a identidade de autenticação (assetUUID puro).

### PKI / Geração de certificados

Os certificados **não** são gerados por este repositório. A PKI da plataforma
é inicializada automaticamente pelo container `mongodb-init` no stack
[mapexOSDeploy](https://github.com/Mapex-Solutions/mapexOSDeploy):

1. `docker compose up -d` executa `mongodb-init`
2. `mongodb-init` gera CA raiz + intermediária + certificado do broker
3. Os certificados do broker são gravados em `./broker-certs/` no host
4. O container do broker monta `./broker-certs:/mosquitto/certs:ro`
5. `entrypoint.sh` detecta automaticamente os certificados e habilita TLS (8883) + mTLS

**O operador nunca gera certificados manualmente.** Para rotacionar,
limpe `./broker-certs/` e reinicie o `mongodb-init`.

### Validando o listener TLS

```bash
# Server-only TLS (server cert verified against system trust store):
mosquitto_pub --cafile /path/to/ca.pem -h broker.example.com -p 8883 \
    -u 'org-1:asset-aaa' -P 'good-pwd' \
    -t 'events/org-1/asset-aaa/x' -m '{"v":1}'

# mTLS (client cert + key required):
mosquitto_pub --cafile ca.pem --cert client.crt --key client.key \
    -h broker.example.com -p 8883 \
    -u 'org-1:asset-aaa' -P 'good-pwd' \
    -t 'events/org-1/asset-aaa/x' -m '{"v":1}'
```

### Healthcheck TLS

O healthcheck padrão verifica o listener plaintext
(`MQTT_LISTENER_PORT`, padrão 1883). Quando você desabilita o plaintext para
um deployment público, sobrescreva o healthcheck no compose:

```yaml
healthcheck:
  test: ["CMD-SHELL", "nc -z 127.0.0.1 ${MQTT_TLS_LISTENER_PORT:-8883} || exit 1"]
```

`nc -z` apenas verifica se a porta TCP está em LISTEN — não verifica o
handshake TLS. Para validação mais profunda em produção, execute um
cliente sintético fora do container.

## O que não é configurável (ainda)

| Concern | Status |
|---|---|
| WebSocket listener | Not in template. Mosquitto supports it natively (`listener 9001` + `protocol websockets`); add by editing the template if needed. |
| Per-listener auth | The plugin's auth chain runs the same on every listener. |
| Auth backend other than TieredAuthStore | Hard-coded — the plugin uses L1 Pebble → L2 MinIO → L3 HTTP. |
| ACL rule customization | Hard-coded in `src/acl.go`. The platform's topic structure is the contract; changing it requires editing + rebuilding. |

Se algum desses itens se tornar um requisito real, abra uma issue descrevendo
o caso de uso antes de adicionar a variável de ambiente — o valor do plugin está
em sua pequena superfície de configuração.

## Volume de persistência

O estado de sessão do Mosquitto para retransmissões QoS 1+ é gravado em
`/mosquitto/data/`. Monte um volume aqui para sobreviver a reinicializações do container:

```yaml
volumes:
  - mosquitto-data:/mosquitto/data
```

Sem um volume, cada reinicialização do container descarta as sessões QoS 1+
em andamento e os clientes precisam se reconectar. Para workloads IoT típicas
(dispositivos usando QoS 0 ou QoS 1 de curta duração) isso é aceitável;
comando/controle de missão crítica deve sempre persistir.

## Healthcheck

O Dockerfile inclui uma sonda TCP contra a porta do listener:

```
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD nc -z 127.0.0.1 ${MQTT_LISTENER_PORT:-1883} || exit 1
```

Se a porta estiver em `LISTEN`, o broker passou pela inicialização do plugin —
NATS conectado, callbacks registrados, plugin pronto. Uma sonda com falha
significa que o broker travou (plugin init retornou non-zero) ou
o listener está vinculado a uma porta não padrão que o operador esqueceu
de alinhar com o healthcheck.

Para executar uma verificação mais profunda em produção (confirmar que o
auth callout realmente funciona), conecte um cliente sintético fora do container e
publique/assine com credenciais conhecidas.

## Checklist de verificação antes de implantar em um novo ambiente

- [ ] `INTERNAL_API_KEY` matches the assets MS configured key
- [ ] `NATS_URL` resolves and is reachable from the broker network
- [ ] `ASSETS_HOST` / `ASSETS_PORT` resolve and are reachable
- [ ] `NATS_SUBJECT_PRESENCE` env-prefix matches the rest of the platform (`prod`, `staging`, `dev`)
- [ ] `NATS_SUBJECT_INGRESS_PREFIX` env-prefix matches
- [ ] Persistent volume mounted on `/mosquitto/data` if you need session durability
- [ ] Image tag pinned (`<YYYY.MM.DD>` or `<vX.Y.Z>`) — never `dev` or `latest` in prod
