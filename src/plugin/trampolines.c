/*
 * Trampolines wired between Mosquitto's C callback registration API
 * and the Go-exported callback bodies (goOn*). The trampolines live
 * in a standalone .c file (rather than the main.go cgo preamble)
 * because cgo compiles the preamble in multiple translation units,
 * which the linker rejects as "multiple definition" when the
 * function bodies are inlined.
 *
 * The Go side declares matching prototypes in main.go's preamble so
 * `C.trampoline_*` resolves; the //export'd goOn* functions are
 * declared `extern` here and defined by cgo.
 */

#include <stdlib.h>
#include <string.h>
#include <mosquitto.h>
#include <mosquitto_broker.h>
#include <openssl/x509.h>
#include <openssl/asn1.h>
#include <openssl/bio.h>

extern int goOnAclCheck(int event, void *event_data, void *userdata);
extern int goOnDisconnect(int event, void *event_data, void *userdata);
extern int goOnMessage(int event, void *event_data, void *userdata);
extern int goOnBasicAuth(int event, void *event_data, void *userdata);

int trampoline_acl_check(int event, void *event_data, void *userdata) {
    return goOnAclCheck(event, event_data, userdata);
}

int trampoline_disconnect(int event, void *event_data, void *userdata) {
    return goOnDisconnect(event, event_data, userdata);
}

int trampoline_message(int event, void *event_data, void *userdata) {
    return goOnMessage(event, event_data, userdata);
}

int trampoline_basic_auth(int event, void *event_data, void *userdata) {
    return goOnBasicAuth(event, event_data, userdata);
}

/*
 * get_cert_serial_hex extracts the serial number from a client's
 * X509 certificate as an uppercase hex string with no separators.
 * Returns a newly-allocated C string the Go side MUST free with
 * free() after copying via C.GoString.
 *
 * Returns NULL when:
 *   - no client cert is attached (mTLS not enabled or device sent
 *     none with TLS_REQUIRE_CLIENT_CERT=false)
 *   - OpenSSL fails to render the serial
 *
 * The serial is naturally a big-endian integer with leading-zero
 * stripping; hex render is deterministic so the Go side comparison
 * against entry.activeCertSerials works without normalization, as
 * long as both sides agree on the format. The assets MS issues
 * certs with the same upper-hex no-separator convention.
 */
char *get_cert_serial_hex(const struct mosquitto *client) {
    if (!client) return NULL;

    /* Mosquitto exposes the verified peer cert via this getter when
     * mTLS is in effect. Returns NULL on plaintext listener or when
     * no cert was presented. */
    const X509 *cert = (const X509 *) mosquitto_client_certificate(client);
    if (!cert) return NULL;

    const ASN1_INTEGER *serial = X509_get0_serialNumber(cert);
    if (!serial) return NULL;

    BIGNUM *bn = ASN1_INTEGER_to_BN(serial, NULL);
    if (!bn) return NULL;

    char *openssl_hex = BN_bn2hex(bn);
    BN_free(bn);
    if (!openssl_hex) return NULL;

    /* Copy into a malloc-allocated buffer so the Go side can free
     * with C.free. BN_bn2hex returns memory allocated via
     * OPENSSL_malloc, which is incompatible with system free(). */
    size_t len = strlen(openssl_hex);
    char *out = (char *) malloc(len + 1);
    if (!out) {
        OPENSSL_free(openssl_hex);
        return NULL;
    }
    memcpy(out, openssl_hex, len + 1);
    OPENSSL_free(openssl_hex);

    /* Uppercase defensively (BN_bn2hex already returns uppercase
     * today, but future OpenSSL versions might change). */
    for (char *p = out; *p; p++) {
        if (*p >= 'a' && *p <= 'f') *p -= 32;
    }
    return out;
}

/*
 * get_cert_cn extracts the Common Name (CN) from a client's X509
 * Subject as a UTF-8 C string. Returns a newly-allocated C string
 * the Go side MUST free with free() after copying via C.GoString.
 *
 * Returns NULL when:
 *   - no client cert is attached (plaintext listener or mTLS opted out)
 *   - the Subject has no CN entry
 *   - OpenSSL fails to render the CN as UTF-8
 *
 * The CN is the canonical device identity in cert mode: the assets MS
 * issues every device cert with Subject.CN = "{orgId}:{assetUUID}", so
 * the Go side parses the CN as if it were the MQTT CONNECT username
 * and resolves to the same (orgId, assetUUID) pair. Letting the cert
 * carry the identity means real-world mTLS devices can CONNECT with
 * an empty username field — the cert is the proof AND the identifier.
 */
char *get_cert_cn(const struct mosquitto *client) {
    if (!client) return NULL;

    const X509 *cert = (const X509 *) mosquitto_client_certificate(client);
    if (!cert) return NULL;

    X509_NAME *subj = X509_get_subject_name(cert);
    if (!subj) return NULL;

    int idx = X509_NAME_get_index_by_NID(subj, NID_commonName, -1);
    if (idx < 0) return NULL;

    X509_NAME_ENTRY *entry = X509_NAME_get_entry(subj, idx);
    if (!entry) return NULL;

    ASN1_STRING *data = X509_NAME_ENTRY_get_data(entry);
    if (!data) return NULL;

    unsigned char *utf8 = NULL;
    int n = ASN1_STRING_to_UTF8(&utf8, data);
    if (n < 0 || utf8 == NULL) return NULL;

    /* Copy into a malloc-allocated buffer so the Go side can free with
     * C.free. ASN1_STRING_to_UTF8 returns memory allocated via
     * OPENSSL_malloc, which is incompatible with system free(). */
    char *out = (char *) malloc((size_t) n + 1);
    if (!out) {
        OPENSSL_free(utf8);
        return NULL;
    }
    memcpy(out, utf8, (size_t) n);
    out[n] = '\0';
    OPENSSL_free(utf8);
    return out;
}
