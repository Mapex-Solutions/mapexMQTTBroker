//go:build cgo_mosquitto_plugin

// Package main — cgo entry point for the MapexOS Mosquitto broker
// plugin. Compiled as a c-shared library (.so) loaded by the broker's
// `plugin /usr/lib/mosquitto/mapex-broker.so` directive.
//
// All branching, parsing, and NATS interaction lives in the bounded-context
// modules (src/modules/*) and the shared kernel (src/packages/*), wired by
// the composition root (src/bootstrap), all of which are unit-testable. This
// file's only job is the cgo glue: the exported C entry points and the
// per-callback orchestration that routes each broker event to the right
// module's service (auth → session → presence / ingress).
//
// The build is gated by the `cgo_mosquitto_plugin` build tag so a default
// `go build ./...` and `go test ./...` run without requiring libmosquitto-dev
// installed; only the Dockerfile multi-stage builder compiles with this tag.
package main

/*
#cgo LDFLAGS: -lmosquitto -lcrypto
#include <stdlib.h>
#include <string.h>
#include <mosquitto.h>
#include <mosquitto_broker.h>
#include <mosquitto_plugin.h>
#include <openssl/x509.h>
#include <openssl/asn1.h>
#include <openssl/bio.h>

// Forward declarations; defined in trampolines.c so the cgo preamble
// can be linked across compilation units without duplicate symbols.
char *get_cert_serial_hex(const struct mosquitto *client);
char *get_cert_cn(const struct mosquitto *client);

// Forward declarations of Go-exported callback bodies. cgo cannot pass
// a Go function pointer directly to C; the trampolines below are C
// functions that immediately call into the Go side.
//
// Note on MOSQ_EVT_CONNECT: not exposed by Mosquitto 2.0.x — only
// added in master / 2.1+. The platform compensates by treating every
// successful BASIC_AUTH callout (handled in this same plugin) as the
// canonical "device just connected" signal — the Go-side Authenticate
// emits presence.connect on the same NATS subject this plugin uses
// for disconnect.
extern int goOnAclCheck(int event, void *event_data, void *userdata);
extern int goOnDisconnect(int event, void *event_data, void *userdata);
extern int goOnMessage(int event, void *event_data, void *userdata);
extern int goOnBasicAuth(int event, void *event_data, void *userdata);

// trampolines defined in trampolines.c — declared here so the Go side
// can take their address via C.trampoline_*. Definitions live in a
// separate .c file because cgo compiles the preamble in multiple
// translation units, causing "multiple definition" linker errors when
// the bodies are inlined here.
int trampoline_acl_check(int event, void *event_data, void *userdata);
int trampoline_disconnect(int event, void *event_data, void *userdata);
int trampoline_message(int event, void *event_data, void *userdata);
int trampoline_basic_auth(int event, void *event_data, void *userdata);
*/
import "C"

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/nats-io/nats.go"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/bootstrap"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth"
	otastatussvc "github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/otastatus/domain/services"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/config"
	"github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/logging"
	natsbus "github.com/Mapex-Solutions/mapexMQTTBroket/src/packages/messaging/nats"
)

// contextWithTimeout returns a context with the supplied deadline,
// or a cancellable background context when timeout is non-positive.
// Centralised so every cgo callback uses the same shape.
func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), timeout)
}

// getClientCertSerial extracts the device's TLS cert serial as an
// uppercase hex string. Returns "" when the device is on the
// plaintext listener OR when mTLS is configured optional and the
// device opted out — both cases route to password-mode auth in the
// runtime.
//
// The C side malloc's the buffer; we copy via C.GoString and free
// the C buffer with C.free.
func getClientCertSerial(client *C.struct_mosquitto) string {
	if client == nil {
		return ""
	}
	cstr := C.get_cert_serial_hex(client)
	if cstr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cstr))
	return C.GoString(cstr)
}

// getClientCertCN extracts the Common Name from the device's TLS
// cert Subject. Returns "" when no cert is attached or the Subject
// has no CN. The Go side uses CN as the username fallback when the
// device CONNECT'd with an empty username — the assets MS issues
// every cert with Subject.CN = bare assetUUID, so the downstream
// parse reuses the auth module username parse without divergence.
func getClientCertCN(client *C.struct_mosquitto) string {
	if client == nil {
		return ""
	}
	cstr := C.get_cert_cn(client)
	if cstr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cstr))
	return C.GoString(cstr)
}

// runtime is the process-wide composition root (bootstrap.App), set in
// mosquitto_plugin_init and read by every callback. atomic.Pointer
// makes the read in callbacks lock-free; nil during pre-init or
// post-cleanup so callbacks defensively no-op.
var runtime atomic.Pointer[bootstrap.App]

// natsConn retains the *nats.Conn so cleanup can Close() it. Keeping
// it separate from the PluginRuntime keeps the runtime Go-pure
// (testable without nats.Conn) — the connection lifecycle is a cgo
// concern.
var natsConn atomic.Pointer[nats.Conn]

// pluginID retains the mosquitto identifier passed at init so cleanup
// can unregister callbacks. The broker would clean up on its own at
// process exit, but explicit unregister is the documented contract.
var pluginID *C.struct_mosquitto_plugin_id_t

// mosquitto_plugin_version is implemented in plugin_version.c so its
// C signature exactly matches the const-qualified prototype declared
// in <mosquitto_plugin.h>. cgo's `//export` cannot generate a `const`
// param declaration, so the compiler rejects the duplicate symbol
// when both prototypes are visible.

//export mosquitto_plugin_init
func mosquitto_plugin_init(
	identifier *C.struct_mosquitto_plugin_id_t,
	userdata *unsafe.Pointer,
	rawOpts *C.struct_mosquitto_opt,
	optCount C.int,
) C.int {
	_ = userdata
	// The plugin now reads its configuration from the environment via the
	// shared mapexGoKit config flow, so Mosquitto's plugin_opt array is no
	// longer consumed for business settings.
	_, _ = rawOpts, optCount
	log := logging.New(nil, logging.LogInfo)
	log.Info("[MODULE:Broker] plugin_init: starting")

	cfg, err := config.Load()
	if err != nil {
		log.Error("[MODULE:Broker] plugin_init: config load failed err=%v", err)
		return C.MOSQ_ERR_UNKNOWN
	}

	nc, err := natsbus.ConnectNATS(cfg.NatsURL, log)
	if err != nil {
		log.Error("[MODULE:Broker] plugin_init: nats connect failed err=%v", err)
		return C.MOSQ_ERR_UNKNOWN
	}

	natsConn.Store(nc)

	rt, err := bootstrap.Build(bootstrap.Options{
		Config:    cfg,
		Publisher: nc,
		NatsConn:  nc,
		Log:       log,
		Deliverer: mosquittoDeliverer{},
	})
	if err != nil {
		log.Error("[MODULE:Broker] plugin_init: app build failed err=%v", err)
		nc.Close()
		return C.MOSQ_ERR_UNKNOWN
	}
	runtime.Store(rt)
	natsConn.Store(nc)
	pluginID = identifier

	if rc := registerCallbacks(identifier); rc != C.MOSQ_ERR_SUCCESS {
		log.Error("[MODULE:Broker] plugin_init: callback registration failed rc=%d", int(rc))
		rt.Shutdown()
		nc.Close()
		runtime.Store(nil)
		natsConn.Store(nil)
		return rc
	}
	log.Info("[MODULE:Broker] plugin_init: ready (4 callbacks registered)")
	return C.MOSQ_ERR_SUCCESS
}

//export mosquitto_plugin_cleanup
func mosquitto_plugin_cleanup(userdata unsafe.Pointer, opts *C.struct_mosquitto_opt, optCount C.int) C.int {
	_, _, _ = userdata, opts, optCount
	rt := runtime.Load()
	if rt == nil {
		return C.MOSQ_ERR_SUCCESS
	}
	rt.Log.Info("[MODULE:Broker] plugin_cleanup: draining")

	if pluginID != nil {
		C.mosquitto_callback_unregister(pluginID, C.MOSQ_EVT_BASIC_AUTH,
			C.MOSQ_FUNC_generic_callback(C.trampoline_basic_auth), nil)
		C.mosquitto_callback_unregister(pluginID, C.MOSQ_EVT_ACL_CHECK,
			C.MOSQ_FUNC_generic_callback(C.trampoline_acl_check), nil)
		C.mosquitto_callback_unregister(pluginID, C.MOSQ_EVT_DISCONNECT,
			C.MOSQ_FUNC_generic_callback(C.trampoline_disconnect), nil)
		C.mosquitto_callback_unregister(pluginID, C.MOSQ_EVT_MESSAGE,
			C.MOSQ_FUNC_generic_callback(C.trampoline_message), nil)
	}

	rt.Shutdown()
	if nc := natsConn.Load(); nc != nil {
		nc.Close()
	}
	runtime.Store(nil)
	natsConn.Store(nil)
	return C.MOSQ_ERR_SUCCESS
}

// registerCallbacks subscribes the four trampolines to their
// respective broker events. MOSQ_EVT_CONNECT is intentionally absent
// — the connect signal flows through the BASIC_AUTH handler in this
// same plugin (see preamble). Returns the first non-success status
// from any registration so the caller can fail init atomically.
func registerCallbacks(id *C.struct_mosquitto_plugin_id_t) C.int {
	pairs := []struct {
		event C.int
		fn    C.MOSQ_FUNC_generic_callback
	}{
		{C.MOSQ_EVT_BASIC_AUTH, C.MOSQ_FUNC_generic_callback(C.trampoline_basic_auth)},
		{C.MOSQ_EVT_ACL_CHECK, C.MOSQ_FUNC_generic_callback(C.trampoline_acl_check)},
		{C.MOSQ_EVT_DISCONNECT, C.MOSQ_FUNC_generic_callback(C.trampoline_disconnect)},
		{C.MOSQ_EVT_MESSAGE, C.MOSQ_FUNC_generic_callback(C.trampoline_message)},
	}
	for _, p := range pairs {
		if rc := C.mosquitto_callback_register(id, p.event, p.fn, nil, nil); rc != C.MOSQ_ERR_SUCCESS {
			return rc
		}
	}
	return C.MOSQ_ERR_SUCCESS
}

//export goOnBasicAuth
func goOnBasicAuth(event C.int, eventData unsafe.Pointer, userdata unsafe.Pointer) C.int {
	_, _ = event, userdata
	rt := runtime.Load()
	if rt == nil || eventData == nil {
		return C.MOSQ_ERR_PLUGIN_DEFER
	}
	ed := (*C.struct_mosquitto_evt_basic_auth)(eventData)

	username := C.GoString(ed.username)
	password := C.GoString(ed.password)
	clientID := C.GoString(C.mosquitto_client_id(ed.client))
	sourceIP := C.GoString(C.mosquitto_client_address(ed.client))

	// Cert serial extraction — only meaningful when mTLS is enabled
	// and the client presented a cert. Returns "" for password-mode
	// devices on the plaintext listener.
	certSerial := getClientCertSerial(ed.client)

	// Cert-mode identity fallback: real mTLS devices often CONNECT
	// with an empty username field — the cert is supposed to carry
	// the identity. The assets MS issues every device cert with
	// Subject.CN = "{orgId}:{assetUUID}", so when cert is present and
	// no username was provided, we hydrate username from the CN. When
	// both are present the device's value is honored as-is (no cross
	// validation: by convention they are equal, and the cert serial
	// check still binds the result to the correct asset).
	if username == "" && certSerial != "" {
		username = getClientCertCN(ed.client)
	}

	// Auth runs SYNCHRONOUSLY on the broker thread (Mosquitto blocks
	// the CONNECT handshake awaiting the decision). The TieredStore
	// keeps L1 hits sub-100µs, L2 hits ~10ms; bcrypt at cost 10
	// (~50ms) dominates the password path regardless.
	ctx, cancel := contextWithTimeout(rt.AuthTimeout)
	defer cancel()
	dec := rt.Auth.Authenticate(ctx, username, password, certSerial)

	switch dec.Result {
	case auth.AuthAllow:
		// On success the entry orchestrates the side effects across
		// modules: record the trusted identity for the later
		// Disconnect / Message callbacks, then emit the presence.connect
		// advisory so the healthmonitor sees the online edge — Mosquitto
		// 2.0.x has no MOSQ_EVT_CONNECT, so this is the canonical signal.
		rt.Session.Remember(clientID, dec.OrgID, dec.AssetUUID)
		rt.Presence.PublishConnect(dec.OrgID, dec.AssetUUID, clientID, sourceIP, time.Now().UTC())
		return C.MOSQ_ERR_SUCCESS
	case auth.AuthDeny:
		return C.MOSQ_ERR_AUTH
	default:
		// AuthError → fail-closed. Returning MOSQ_ERR_UNKNOWN tells
		// the broker to deny the CONNECT. Operators see the cause
		// in the plugin's WARN log lines.
		return C.MOSQ_ERR_UNKNOWN
	}
}

//export goOnAclCheck
func goOnAclCheck(event C.int, eventData unsafe.Pointer, userdata unsafe.Pointer) C.int {
	_, _ = event, userdata
	rt := runtime.Load()
	if rt == nil || eventData == nil {
		return C.MOSQ_ERR_PLUGIN_DEFER
	}
	ed := (*C.struct_mosquitto_evt_acl_check)(eventData)

	username := C.GoString(C.mosquitto_client_username(ed.client))
	topic := C.GoString(ed.topic)
	access := int(ed.access)

	if rt.Auth.Authorize(username, topic, access) {
		return C.MOSQ_ERR_SUCCESS
	}
	return C.MOSQ_ERR_ACL_DENIED
}

//export goOnDisconnect
func goOnDisconnect(event C.int, eventData unsafe.Pointer, userdata unsafe.Pointer) C.int {
	_, _ = event, userdata
	rt := runtime.Load()
	if rt == nil || eventData == nil {
		return C.MOSQ_ERR_SUCCESS
	}
	ed := (*C.struct_mosquitto_evt_disconnect)(eventData)

	clientID := C.GoString(C.mosquitto_client_id(ed.client))
	sourceIP := C.GoString(C.mosquitto_client_address(ed.client))

	info, ok := rt.Session.Lookup(clientID)
	if !ok {
		// No prior Authenticate / unknown clientID — service account
		// or stale callback. Nothing to publish, nothing to clean up.
		return C.MOSQ_ERR_SUCCESS
	}
	rt.Presence.PublishDisconnect(info.OrgID, info.AssetUUID, clientID, sourceIP, int(ed.reason), time.Now().UTC())
	rt.Session.Forget(clientID)
	return C.MOSQ_ERR_SUCCESS
}

//export goOnMessage
func goOnMessage(event C.int, eventData unsafe.Pointer, userdata unsafe.Pointer) C.int {
	_, _ = event, userdata
	rt := runtime.Load()
	if rt == nil || eventData == nil {
		return C.MOSQ_ERR_SUCCESS
	}
	ed := (*C.struct_mosquitto_evt_message)(eventData)

	clientID := C.GoString(C.mosquitto_client_id(ed.client))
	topic := C.GoString(ed.topic)

	// C.GoBytes COPIES — the broker frees the source buffer after
	// our callback returns, so we must own the bytes by the time
	// they reach the worker goroutine.
	payload := C.GoBytes(ed.payload, C.int(ed.payloadlen))

	info, ok := rt.Session.Lookup(clientID)
	if !ok {
		return C.MOSQ_ERR_SUCCESS
	}

	// OTA status reports (events/{assetUUID}/ota_status) are control
	// messages, not telemetry: they become the OTA advisory and skip the
	// uplink ingress pipeline. Identity comes from the session, never the
	// topic/payload.
	if otastatussvc.IsStatusTopic(topic) {
		rt.OTAStatus.HandleStatusReport(info.OrgID, info.AssetUUID, payload, time.Now().UTC())
		return C.MOSQ_ERR_SUCCESS
	}

	rt.Ingress.Publish(info.OrgID, info.AssetUUID, clientID, topic, payload,
		int(ed.qos), bool(ed.retain), time.Now().UTC())
	return C.MOSQ_ERR_SUCCESS
}

// mosquittoDeliverer implements the downlink Deliverer over
// mosquitto_broker_publish_copy (the broker copies the payload, so the Go
// slice stays owned by Go). clientid=NULL publishes broker-wide to the topic;
// the ACL guarantees only the device whose assetUUID is in the topic can be
// subscribed, so broker-wide IS device-targeted.
type mosquittoDeliverer struct{}

// Deliver publishes the payload to the broker-local topic. Called from the
// NATS consumer goroutine — mosquitto_broker_publish_copy queues the message
// into the broker loop.
func (mosquittoDeliverer) Deliver(topic string, payload []byte, qos int) error {
	ctopic := C.CString(topic)
	defer C.free(unsafe.Pointer(ctopic))

	var payloadPtr unsafe.Pointer
	if len(payload) > 0 {
		payloadPtr = unsafe.Pointer(&payload[0])
	}

	rc := C.mosquitto_broker_publish_copy(nil, ctopic, C.int(len(payload)), payloadPtr, C.int(qos), C._Bool(false), nil)
	if rc != C.MOSQ_ERR_SUCCESS {
		return fmt.Errorf("mosquitto_broker_publish_copy rc=%d topic=%s", int(rc), topic)
	}
	return nil
}

// main is required by cgo c-shared mode but is never executed —
// .so files don't have an entry point.
func main() {}
