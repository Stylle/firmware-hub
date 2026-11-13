#if defined(__MINGW32__) && !defined(__USE_MINGW_ANSI_STDIO)
#define __USE_MINGW_ANSI_STDIO 1
#endif
#include "fw_client.h"
#include <limits.h>
#include <stdio.h>
#include <string.h>

static int identifier(const char *s) {
    size_t n = 0;
    if (!s)
        return 0;
    for (; s[n]; n++) {
        unsigned char c = (unsigned char)s[n];
        int alpha = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9');
        if (n >= 64 || (!alpha && (n == 0 || (c != '.' && c != '_' && c != '+' && c != '-'))))
            return 0;
    }
    return n > 0;
}
static int number(const char *s, uint64_t *n) {
    uint64_t x = 0;
    size_t i;
    if (!s[0] || (s[0] == '0' && s[1]))
        return 0;
    for (i = 0; s[i]; i++) {
        unsigned d = (unsigned)(s[i] - '0');
        if (d > 9 || x > ((uint64_t)INT64_MAX - d) / 10u)
            return 0;
        x = x * 10u + d;
    }
    *n = x;
    return 1;
}
static int nibble(char c) {
    if (c >= '0' && c <= '9')
        return c - '0';
    if (c >= 'a' && c <= 'f')
        return c - 'a' + 10;
    if (c >= 'A' && c <= 'F')
        return c - 'A' + 10;
    return -1;
}
fw_result fw_decode_hex(const char *hex, size_t n, uint8_t *out, size_t bytes) {
    size_t i;
    if (!hex || !out || bytes > SIZE_MAX / 2 || n != bytes * 2)
        return FW_INVALID_ARGUMENT;
    for (i = 0; i < bytes; i++) {
        int a = nibble(hex[i * 2]), b = nibble(hex[i * 2 + 1]);
        if (a < 0 || b < 0)
            return FW_FORMAT_ERROR;
        out[i] = (uint8_t)(a * 16 + b);
    }
    return FW_OK;
}
static int match(const char *target, const char *device) {
    return strcmp(target, "any") == 0 || strcmp(target, device) == 0;
}
static int valid_policy(const fw_policy *p) {
    return p && identifier(p->project) && identifier(p->channel) && identifier(p->platform) &&
           identifier(p->arch) && identifier(p->hardware);
}

fw_result fw_accept_server_manifest(const uint8_t *payload, size_t length, const fw_policy *p,
                                    fw_manifest *out) {
    char buf[FW_PAYLOAD_MAX + 1], *fields[13];
    size_t i, start = 0, count = 0;
    fw_manifest m;
    if (out)
        memset(out, 0, sizeof(*out));
    if (!payload || !out || !valid_policy(p) || !p->maximum_size)
        return FW_INVALID_ARGUMENT;
    if (length == 0 || length > FW_PAYLOAD_MAX || payload[length - 1] != '\n')
        return FW_FORMAT_ERROR;
    memcpy(buf, payload, length);
    buf[length] = 0;
    for (i = 0; i < length; i++) {
        if (!buf[i])
            return FW_FORMAT_ERROR;
        if (buf[i] == '\n') {
            if (count >= 13)
                return FW_FORMAT_ERROR;
            buf[i] = 0;
            fields[count++] = buf + start;
            start = i + 1;
        }
    }
    if (count != 13 || start != length || strcmp(fields[0], "FH1"))
        return FW_FORMAT_ERROR;
    memset(&m, 0, sizeof(m));
    {
        char *dst[] = {m.project, m.version, m.channel, m.platform, m.arch, m.hardware};
        size_t src[] = {1, 2, 5, 6, 7, 8};
        for (i = 0; i < 6; i++) {
            if (!identifier(fields[src[i]]))
                return FW_FORMAT_ERROR;
            strcpy(dst[i], fields[src[i]]);
        }
    }
    if (!number(fields[3], &m.sequence) || !m.sequence || !number(fields[4], &m.security) ||
        !number(fields[10], &m.size) || !m.size || !number(fields[11], &m.min_sequence) ||
        !number(fields[12], &m.expires) || !m.expires)
        return FW_FORMAT_ERROR;
    if (strlen(fields[9]) != 64 || fw_decode_hex(fields[9], 64, m.sha256, 32) != FW_OK)
        return FW_FORMAT_ERROR;
    for (i = 0; i < 64; i++)
        if (fields[9][i] >= 'A' && fields[9][i] <= 'F')
            return FW_FORMAT_ERROR;
    if (strcmp(m.project, p->project) || strcmp(m.channel, p->channel) ||
        !match(m.platform, p->platform) || !match(m.arch, p->arch) ||
        !match(m.hardware, p->hardware) || m.min_sequence > p->current_sequence ||
        m.size > p->maximum_size)
        return FW_INCOMPATIBLE;
    if (m.security < p->minimum_security || m.sequence <= p->current_sequence ||
        m.sequence < p->highest_seen_sequence)
        return FW_ROLLBACK;
    if (!p->unix_time)
        return FW_CLOCK_REQUIRED;
    if (m.expires <= p->unix_time)
        return FW_EXPIRED;
    *out = m;
    return FW_OK;
}
fw_result fw_verify_manifest(const uint8_t *payload, size_t length, const uint8_t signature[64],
                             const uint8_t key[32], const fw_policy *p, fw_ed25519_verify_fn verify,
                             void *ctx, fw_manifest *out) {
    if (out)
        memset(out, 0, sizeof(*out));
    if (!payload || !signature || !key || !out || !verify || !valid_policy(p) || !p->maximum_size)
        return FW_INVALID_ARGUMENT;
    if (length == 0 || length > FW_PAYLOAD_MAX || payload[length - 1] != '\n')
        return FW_FORMAT_ERROR;
    if (verify(ctx, key, signature, payload, length) != 0)
        return FW_SIGNATURE_ERROR;
    return fw_accept_server_manifest(payload, length, p, out);
}
fw_result fw_verify_artifact(const fw_manifest *m, const uint8_t actual[32], uint64_t size) {
    unsigned diff = 0;
    size_t i;
    if (!m || !actual || !m->size || !m->sequence)
        return FW_INVALID_ARGUMENT;
    for (i = 0; i < 32; i++)
        diff |= (unsigned)(m->sha256[i] ^ actual[i]);
    return diff || size != m->size ? FW_INTEGRITY_ERROR : FW_OK;
}
static void query_id(char out[193], const char *in) {
    size_t n = 0;
    while (*in) {
        if (*in == '+') {
            out[n++] = '%';
            out[n++] = '2';
            out[n++] = 'B';
        } else
            out[n++] = *in;
        ++in;
    }
    out[n] = 0;
}
fw_result fw_make_check_path(const fw_policy *p, char *out, size_t capacity) {
    int n;
    if (!out || !capacity || !valid_policy(p))
        return FW_INVALID_ARGUMENT;
    char channel[193], platform[193], arch[193], hardware[193], version[193];
    if (p->current_version && !identifier(p->current_version))
        return FW_INVALID_ARGUMENT;
    query_id(channel, p->channel);
    query_id(platform, p->platform);
    query_id(arch, p->arch);
    query_id(hardware, p->hardware);
    query_id(version, p->current_version ? p->current_version : "");
    n = snprintf(
        out, capacity,
        "/api/v1/update/%s/"
        "check?channel=%s&platform=%s&arch=%s&hardware=%s&sequence=%llu&security=%llu&version=%s",
        p->project, channel, platform, arch, hardware, (unsigned long long)p->current_sequence,
        (unsigned long long)p->minimum_security, version);
    return n < 0 || (size_t)n >= capacity ? FW_BUFFER_TOO_SMALL : FW_OK;
}
fw_result fw_check(const fw_policy *p, fw_http_fn http, void *ctx, char *response, size_t capacity,
                   size_t *response_size) {
    char path[1024];
    fw_result e;
    if (!http || !response || !response_size || !capacity)
        return FW_INVALID_ARGUMENT;
    e = fw_make_check_path(p, path, sizeof(path));
    if (e != FW_OK)
        return e;
    *response_size = 0;
    if (http(ctx, path, NULL, response, capacity, response_size) != 0 || *response_size > capacity)
        return FW_TRANSPORT_ERROR;
    return FW_OK;
}
fw_result fw_heartbeat(const fw_policy *p, const char *version, const char *status, fw_http_fn http,
                       void *ctx) {
    char path[160], body[512], response[1024];
    size_t size = 0;
    int n;
    if (!valid_policy(p) || !http || !identifier(version) || !status)
        return FW_INVALID_ARGUMENT;
    if (!p->telemetry_enabled || !p->hub_telemetry_enabled)
        return FW_DISABLED;
    if (strcmp(status, "idle") && strcmp(status, "downloading") && strcmp(status, "verified") &&
        strcmp(status, "installing") && strcmp(status, "confirmed") && strcmp(status, "failed"))
        return FW_INVALID_ARGUMENT;
    n = snprintf(path, sizeof(path), "/api/v1/update/%s/heartbeat", p->project);
    if (n < 0 || (size_t)n >= sizeof(path))
        return FW_BUFFER_TOO_SMALL;
    n = snprintf(body, sizeof(body),
                 "{\"version\":\"%s\",\"platform\":\"%s\",\"arch\":\"%s\",\"hardware\":\"%s\","
                 "\"status\":\"%s\"}",
                 version, p->platform, p->arch, p->hardware, status);
    if (n < 0 || (size_t)n >= sizeof(body))
        return FW_BUFFER_TOO_SMALL;
    return http(ctx, path, body, response, sizeof(response), &size) == 0 && size <= sizeof(response)
               ? FW_OK
               : FW_TRANSPORT_ERROR;
}
const char *fw_result_string(fw_result r) {
    switch (r) {
    case FW_OK:
        return "OK";
    case FW_INVALID_ARGUMENT:
        return "Invalid argument";
    case FW_FORMAT_ERROR:
        return "Malformed FH1 manifest";
    case FW_SIGNATURE_ERROR:
        return "Invalid Ed25519 signature";
    case FW_INCOMPATIBLE:
        return "Incompatible project, platform, slot or prerequisite";
    case FW_ROLLBACK:
        return "Rollback or non-newer release refused";
    case FW_EXPIRED:
        return "Manifest expired";
    case FW_CLOCK_REQUIRED:
        return "Trusted clock required";
    case FW_INTEGRITY_ERROR:
        return "Artifact hash or size mismatch";
    case FW_BUFFER_TOO_SMALL:
        return "Buffer too small";
    case FW_TRANSPORT_ERROR:
        return "Transport failed";
    case FW_DISABLED:
        return "Telemetry disabled; no network request sent";
    default:
        return "Unknown error";
    }
}
