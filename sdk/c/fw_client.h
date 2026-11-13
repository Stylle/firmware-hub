/**
 * @file fw_client.h
 * @brief Firmware Hub FH1 portable C99 client and verification policy.
 * @details No socket, heap allocation, flash write, or private key is hidden in
 * this module. The application owns transport, trusted public key, clock,
 * persistent anti-rollback counters and the bootloader integration.
 * @warning FH1 is not a TUF/SUIT implementation. TLS and a valid SHA-256 alone
 * are insufficient: call fw_verify_manifest() and fw_verify_artifact().
 */
#ifndef FW_CLIENT_H
#define FW_CLIENT_H
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif

#define FW_ID_CAP 65u
#define FW_PAYLOAD_MAX 1024u
/** Result codes. No nonzero result permits installing an update. */
typedef enum {
    FW_OK = 0,
    FW_INVALID_ARGUMENT = -1,
    FW_FORMAT_ERROR = -2,
    FW_SIGNATURE_ERROR = -3,
    FW_INCOMPATIBLE = -4,
    FW_ROLLBACK = -5,
    FW_EXPIRED = -6,
    FW_CLOCK_REQUIRED = -7,
    FW_INTEGRITY_ERROR = -8,
    FW_BUFFER_TOO_SMALL = -9,
    FW_TRANSPORT_ERROR = -10,
    FW_DISABLED = -11
} fw_result;

/** Verified release properties. Strings are always NUL-terminated on success. */
typedef struct {
    char project[FW_ID_CAP], version[FW_ID_CAP], channel[FW_ID_CAP];
    char platform[FW_ID_CAP], arch[FW_ID_CAP], hardware[FW_ID_CAP];
    uint64_t sequence, security, size, min_sequence, expires;
    uint8_t sha256[32];
} fw_manifest;

/** Device-controlled policy, never populated from untrusted server fields. */
typedef struct {
    const char *project, *channel, *platform, *arch, *hardware;
    uint64_t current_sequence;      /**< Currently installed release sequence. */
    uint64_t minimum_security;      /**< Persisted security floor; never decrease. */
    uint64_t highest_seen_sequence; /**< Persisted verified metadata floor. */
    uint64_t unix_time;             /**< Trusted UTC seconds; zero fails closed. */
    uint64_t maximum_size;          /**< Available slot/file capacity; must be nonzero. */
    int telemetry_enabled;          /**< Local opt-in. Default zero; no report is sent. */
    int hub_telemetry_enabled;      /**< Last check response; default zero. */
} fw_policy;

/** Ed25519 backend. Return 0 only for a valid standard Ed25519 signature.
 * @param ctx Application-owned backend context; may be NULL.
 * @param key The pre-provisioned trusted key, not one downloaded with firmware.
 * @note Use a vetted cryptographic library, not a custom implementation.
 */
typedef int (*fw_ed25519_verify_fn)(void *ctx, const uint8_t key[32], const uint8_t signature[64],
                                    const uint8_t *payload, size_t length);

/** HTTP adapter. Return 0 only for a successful HTTP 2xx response.
 * The adapter must enforce TLS certificates/hostname, response bounds, timeout,
 * and safe redirect handling. Never forward authorization across origins.
 * The response buffer belongs to the caller. Set *response_size to actual size.
 * A NULL request_body means GET; otherwise send application/json with POST.
 */
typedef int (*fw_http_fn)(void *ctx, const char *relative_path, const char *request_body,
                          char *response, size_t capacity, size_t *response_size);

/** Verify the exact FH1 payload and signature, then enforce device policy.
 * @return FW_OK only when every check succeeds. out is zeroed on failure.
 * @note Persist highest_seen_sequence after successful verification. Advance
 * installed/security state only according to your bootloader confirmation rules.
 */
fw_result fw_verify_manifest(const uint8_t *payload, size_t length, const uint8_t signature[64],
                             const uint8_t trusted_key[32], const fw_policy *policy,
                             fw_ed25519_verify_fn verify, void *crypto_ctx, fw_manifest *out);

/** Compare the digest and exact length of a fully downloaded artifact.
 * @warning A digest is trusted only after verifying its manifest signature.
 */
fw_result fw_verify_artifact(const fw_manifest *manifest, const uint8_t actual_sha256[32],
                             uint64_t actual_size);

/** Build the update-check URL. IDs are validated and need no URL escaping. */
fw_result fw_make_check_path(const fw_policy *policy, char *out, size_t capacity);

/** Perform a check using a supplied transport. Decode the bounded JSON response
 * in the platform adapter; base64-decode release.payload, hex-decode signature,
 * then call fw_verify_manifest(). Never install from JSON alone.
 */
fw_result fw_check(const fw_policy *policy, fw_http_fn http, void *ctx, char *response,
                   size_t capacity, size_t *response_size);

/** Send a version/heartbeat report only if both local and Hub opt-ins are set.
 * @param status One of idle/downloading/verified/installing/confirmed/failed.
 * @return FW_DISABLED without calling transport if either switch is off.
 */
fw_result fw_heartbeat(const fw_policy *policy, const char *current_version, const char *status,
                       fw_http_fn http, void *ctx);

/** Decode lowercase/uppercase hexadecimal. Input must be exactly 2*out_size. */
fw_result fw_decode_hex(const char *hex, size_t length, uint8_t *out, size_t out_size);
/** Return a static, non-sensitive description of a result code. */
const char *fw_result_string(fw_result result);
#ifdef __cplusplus
}
#endif
#endif
