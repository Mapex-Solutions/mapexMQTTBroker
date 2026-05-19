# Deploy do Mapex MQTT Broker

Runbook operacional para subir o broker com TLS/mTLS habilitado.

## Pré-requisitos

A stack [mapexOSDeploy](https://github.com/Mapex-Solutions/mapexOSDeploy)
cuida de tudo. Não é necessário gerar PKI manualmente.

## Passos do deploy

Diretório de trabalho: `mapexOSDeploy/`.

1. Clone e inicie:
   ```bash
   git clone https://github.com/Mapex-Solutions/mapexOSDeploy.git
   cd mapexOSDeploy
   docker compose up -d
   ```

2. Aguarde ~2 minutos. O container `mongodb-init`:
   - Inicializa o replica set do MongoDB
   - Gera a PKI da plataforma (CA raiz + intermediária + certificado do broker)
   - Escreve `server.crt`, `server.key`, `ca-chain.pem` em `./broker-certs/`
   - Criptografa as chaves da CA via envelope e as insere no banco do mapexVault
   - Popula os dados iniciais (usuários, organizações, papéis)

3. O broker inicia após a conclusão do `mongodb-init` e detecta o TLS automaticamente:
   ```bash
   docker compose logs mapex-mqtt-broker | grep TLS
   ```
   Saída esperada:
   ```
   [ENTRYPOINT] TLS auto-detected: server.crt + server.key present, enabling listener 8883
   [ENTRYPOINT] mTLS auto-detected: /mosquitto/certs/ca.pem present, require_certificate=true
   ```

## Flags de substituição (env)

| Env | Padrão | Finalidade |
|---|---|---|
| `TLS_ENABLED=false` | não definido (auto-detecção) | Desabilita o TLS à força mesmo quando os arquivos de certificado estão montados |
| `TLS_REQUIRE_CLIENT_CERT=false` | `true` | Desabilita o mTLS mantendo o listener TLS ativo |

## Renovação

Limpe os certificados do broker e reinicie o `mongodb-init` para regenerá-los:

```bash
rm -rf ./broker-certs/*
docker compose up -d mongodb-init --force-recreate
docker compose restart mapex-mqtt-broker
```

O container `mongodb-init` é idempotente — ao ser reexecutado com
`broker-certs/` vazio, ele gera novo material. Os certificados de
dispositivos existentes continuam válidos porque a CA permanece a mesma
(armazenada no Mongo).

Rotacionar a CA da plataforma em si requer limpar a coleção
`mapex_vault.pkiCertificateAuthorities` antes de executar o
`mongodb-init`; isso está fora do escopo do fluxo padrão de renovação.
