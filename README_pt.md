# Mapex MQTT Broker

> Broker MQTT de produção para a plataforma
> [MapexOS](https://github.com/Mapex-Solutions/mapexOS) — Eclipse
> Mosquitto v2 + um plugin próprio que lida com **auth**, **ACL**,
> **presença** e **ingresso** em um único binário Go. Distribuído
> como imagem Docker pronta para uso (`mapexos/mapex-broker-mqtt`).

### Sobre o MapexOS

> **IoT-first, mas não se limita a IoT.**
> O MapexOS não vê dispositivos ou sensores — ele vê **Assets**.
> Qualquer fonte. Qualquer protocolo. Uma única abstração.
>
> **Connect. Automate. Scale.** — A plataforma aberta para integração de
> dados e automação inteligente.

```
   Fontes                        MapexOS                          Destinos
   ──────                        ───────                          ────────
   Devices ──┐                                              ┌── Webhooks / APIs
   Gateways ─┤   Ingest → Validate → Transform → Route →    ├── Slack / Teams / Email
   APIs ─────┼──        Store / Notify / Automate           ├── NATS / MQTT
   Apps ─────┤                                              └── Plugins customizados
   Terceiros ┘
```

Este broker fica na **borda de ingresso MQTT** desse pipeline.

[English version](./README.md) · [Site de documentação](https://mapexos.io)

## O que é este projeto

Um plugin Mosquitto que transforma um broker MQTT padrão na borda do
MapexOS — cada CONNECT de dispositivo, cada PUBLISH autorizado, cada
DISCONNECT se torna um evento NATS estruturado consumível pelo resto
da plataforma.

| Responsabilidade | Onde executa |
|---|---|
| **Auth** (cache em camadas) | `MOSQ_EVT_BASIC_AUTH` neste plugin → TieredAuthStore (L1 Pebble → L2 MinIO → L3 HTTP fallback) |
| **ACL** (permitir/negar PUB+SUB) | `MOSQ_EVT_ACL_CHECK` neste plugin → comparação pure-Go, sub-µs, sem chamada de rede |
| **Presença** (online/offline) | `MOSQ_EVT_DISCONNECT` + auth-success → NATS `presence.advisory` |
| **Ingresso** (dispositivo → plataforma) | `MOSQ_EVT_MESSAGE` → NATS `mqtt.data.{orgId}.{assetUUID}` |

Um único `.so`, quatro hooks do broker, dois subjects NATS. Sem
round-trips HTTP no caminho quente de auth — cada decisão de CONNECT
(bcrypt para modo senha, igualdade de serial do certificado para modo
certificado) é feita localmente a partir da projeção `AuthEntry` em
cache.

### Modos de autenticação

Cada asset declara um modo de auth; o broker impõe exclusão mútua:

| Modo | Credenciais no fio | Validação |
|---|---|---|
| **password** | MQTT username (assetUUID) + senha | Comparação bcrypt contra `AuthEntry.PasswordHash` |
| **cert** | MQTT username (assetUUID) + certificado do cliente no listener TLS (8883) | Igualdade de serial contra `AuthEntry.CurrentCertSerial` |

Um asset em modo senha que apresenta certificado é negado. Um asset em
modo certificado que conecta no listener plaintext é negado. O broker
nunca adivinha qual credencial validar.

### TieredAuthStore

O plugin faz **zero chamadas HTTP de auth** no caminho quente. As
decisões de auth são servidas por um cache de três camadas:

| Camada | Backend | Latência | Observações |
|---|---|---|---|
| **L1** | Pebble em NVMe | ~50µs | Persiste entre reinícios do plugin quando o volume está montado |
| **L2** | Bucket MinIO `mapex-asset-auth` | ~10ms | `AuthProjection` slim escrita pelo assets MS a cada CRUD |
| **L3** | HTTP GET para assets MS `/internal/assets/:assetUUID` | ~50ms | Último recurso quando L1 + L2 falham |

Auto-recuperação: cada hit L2 aquece L1; cada hit L3 aquece L1. O
assets MS alimenta L2 a cada CRUD. Um consumer NATS FANOUT
(`mapexos.fanout.asset.invalidate`) invalida entradas L1 obsoletas para
que o próximo CONNECT busque de L2. O TTL do L1 (padrão 30min) é uma
rede de segurança para invalidações perdidas.

O container expõe tanto a porta MQTT plaintext (`1883`) quanto a porta
TLS (`8883`). TLS é opt-in via `TLS_ENABLED=true` e suporta mTLS
opcional — veja [docs/config.md](docs/config.md) para a montagem de
certificados + variáveis de ambiente.

## Por que um repositório dedicado

Este broker é infraestrutura compartilhada consumida por todo deploy
MapexOS. Ter seu próprio repositório permite:

- Versionar + taguear independentemente dos repositórios de serviço.
- Publicar em um Docker registry (`mapexos/mapex-broker-mqtt:<tag>`)
  para que operadores **apenas puxem**, sem precisar buildar.
- Ter seu próprio ciclo de CI (build → test → push) sem acoplamento
  aos pipelines goKit / mapexOS.

## Estrutura

```
.
├── README.md              este arquivo
├── Makefile               targets de build / push / release
├── go.mod                 módulo: github.com/Mapex-Solutions/mapexMQTTBroket
├── src/
│   ├── *.go               pacote broker: ACL, TieredAuthStore, publisher NATS, config
│   ├── *_test.go          testes unitários pure-Go (sem broker, sem NATS)
│   └── plugin/
│       ├── main.go        entry cgo — lifecycle do plugin + 4 hooks
│       ├── plugin_version.c
│       └── trampolines.c  pontes C entre callbacks mosquitto e Go
├── config/
│   └── mosquitto.conf.template   renderizado pelo entrypoint.sh no start do container
├── docker/
│   ├── Dockerfile         builder multi-stage → mapexos/mapex-broker-mqtt
│   └── entrypoint.sh      envsubst + validação + exec mosquitto
├── scripts/
│   └── release/start.sh   workflow de build + tag + push
├── docs/
│   ├── architecture.md    como o plugin funciona internamente
│   ├── config.md          referência de variáveis de ambiente
│   └── deploy.md          runbook de deploy para operadores
└── tests/
    └── smoke/             smoke test mínimo (NATS + MinIO + broker)
```

Os fontes Go ficam em `src/` por convenção do projeto. Dois caminhos
de import importam:

- `github.com/Mapex-Solutions/mapexMQTTBroket/src` — pacote broker
  (pure-Go, totalmente testável)
- `github.com/Mapex-Solutions/mapexMQTTBroket/src/plugin` — entry
  point cgo (controlado pela build tag `cgo_mosquitto_plugin`)

## Executando a imagem publicada

```yaml
# docker-compose.yml
services:
  mapex-broker-mqtt:
    image: docker.io/mapexos/mapex-broker-mqtt:dev
    ports:
      - "1883:1883"
    environment:
      INTERNAL_API_KEY: ${INTERNAL_API_KEY:?required}
      NATS_URL: nats://nats-core:4222
      ASSETS_HOST: assets
      ASSETS_PORT: "5002"
      NATS_SUBJECT_PRESENCE: dev.mapexos.presence.advisory
      NATS_SUBJECT_INGRESS_PREFIX: dev.mapexos.mqtt.data
    volumes:
      - mqtt-cache:/var/cache/mqtt    # persistência L1 Pebble
    depends_on:
      nats-core:
        condition: service_healthy
      assets:
        condition: service_started
```

Operadores não precisam buildar nada. A imagem contém o broker, o
plugin e uma configuração templateada renderizada a partir de variáveis
de ambiente na inicialização. A referência completa de variáveis está em
[docs/config.md](docs/config.md). O fluxo interno de requisições,
modelo de threading e modos de falha estão em
[docs/architecture.md](docs/architecture.md).

## Build local

O `go build ./...` padrão exclui o entry cgo (build tag), então o
pacote broker compila + testa em qualquer host sem
`libmosquitto-dev`:

```bash
make test    # go test -race ./src/...
```

Para buildar o `.so` real, você precisa dos headers do broker; o
caminho fácil é o Dockerfile, que os instala no estágio builder:

```bash
make build VERSION=dev
```

Isso produz `docker.io/mapexos/mapex-broker-mqtt:dev` pronto para
rodar. Sobrescreva `REGISTRY` e `VERSION` para publicar:

```bash
docker login docker.io
make release VERSION=2026.05.08
```

`release` cross-compila para `linux/amd64` + `linux/arm64` via buildx
e pusha `:VERSION` + `:latest` em um único comando.

## Matriz de compatibilidade

| Componente | Fixado em |
|---|---|
| Mosquitto | 2.0.x (pacote Debian bookworm) |
| Plugin API | v5 |
| NATS server | 2.10+ (Core Pub/Sub usado; JetStream opcional, capturado upstream) |
| Go | 1.25.3+ |
| Base runtime | `debian:bookworm-slim` (glibc; Alpine quebra cgo TLS) |

## Convenção de tags

| Tag | Origem | Tempo de vida |
|---|---|---|
| `dev` | builds de branches feature/integração | sobrescrita a cada push |
| `<YYYY.MM.DD>` | builds de branch de release | imutável |
| `<vX.Y.Z>` | tags semver do git | imutável |
| `latest` | release estável mais recente | flutuante |

## Licença

Proprietário — Mapex Solutions.
