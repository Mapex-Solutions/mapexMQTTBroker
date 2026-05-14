/*
 * mosquitto_plugin_version — required entry point the broker calls
 * via dlsym on plugin load to negotiate the plugin API version.
 *
 * Implemented here (not in Go via //export) because the broker's
 * header prototype uses `const int *supported_versions` and cgo
 * cannot generate `const` qualifiers from Go signatures, producing
 * a "conflicting types" compile error when both prototypes are in
 * scope.
 *
 * The file is picked up automatically by the cgo build because it
 * lives in the same package directory as the .go entry point.
 *
 * Returns 5 — Mosquitto plugin v5 API. Older brokers reject; 2.x+
 * accept.
 */
#include <mosquitto.h>
#include <mosquitto_broker.h>
#include <mosquitto_plugin.h>

mosq_plugin_EXPORT int mosquitto_plugin_version(int supported_version_count, const int *supported_versions) {
    (void)supported_version_count;
    (void)supported_versions;
    return 5;
}
