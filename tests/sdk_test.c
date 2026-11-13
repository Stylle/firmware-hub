#include "fw_client.h"
#include "monocypher-ed25519.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>
static int calls = 0;
static int verify(void *ctx, const uint8_t key[32], const uint8_t sig[64], const uint8_t *p,
                  size_t n) {
    (void)ctx;
    return crypto_ed25519_check(sig, key, p, n);
}
static int transport(void *ctx, const char *path, const char *body, char *response, size_t capacity,
                     size_t *size) {
    (void)ctx;
    (void)path;
    (void)body;
    (void)response;
    (void)capacity;
    *size = 0;
    calls++;
    return 0;
}
int main(int argc, char **argv) {
    uint8_t vector[1200], sha[32];
    fw_manifest m;
    fw_policy p = {0};
    size_t n;
    FILE *f;
    char path[512];
    assert(argc == 2);
    f = fopen(argv[1], "rb");
    assert(f);
    n = fread(vector, 1, sizeof(vector), f);
    fclose(f);
    assert(n > 96 && n < sizeof(vector));
    p.project = "demo";
    p.channel = "stable";
    p.platform = "test";
    p.arch = "x64";
    p.hardware = "demo";
    p.unix_time = 1900000000;
    p.maximum_size = 1024;
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_OK);
    assert(m.sequence == 2 && m.security == 1 && m.size == 100);
    assert(fw_accept_server_manifest(vector + 96, n - 96, &p, &m) == FW_OK);
    memset(sha, 0xab, sizeof(sha));
    assert(fw_verify_artifact(&m, sha, 100) == FW_OK);
    sha[0] ^= 1;
    assert(fw_verify_artifact(&m, sha, 100) == FW_INTEGRITY_ERROR);
    sha[0] ^= 1;
    assert(fw_verify_artifact(&m, sha, 99) == FW_INTEGRITY_ERROR);
    vector[32] ^= 1;
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_SIGNATURE_ERROR);
    assert(m.sequence == 0);
    vector[32] ^= 1;
    p.project = "wrong";
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_INCOMPATIBLE);
    p.project = "demo";
    p.arch = "arm";
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_INCOMPATIBLE);
    p.arch = "x64";
    p.unix_time = 0;
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_CLOCK_REQUIRED);
    p.unix_time = 2000000000;
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_EXPIRED);
    p.unix_time = 1900000000;
    p.current_sequence = 2;
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_ROLLBACK);
    p.current_sequence = 0;
    p.minimum_security = 2;
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_ROLLBACK);
    p.minimum_security = 0;
    p.highest_seen_sequence = 3;
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_ROLLBACK);
    p.highest_seen_sequence = 0;
    p.maximum_size = 99;
    assert(fw_verify_manifest(vector + 96, n - 96, vector + 32, vector, &p, verify, NULL, &m) ==
           FW_INCOMPATIBLE);
    p.maximum_size = 1024;
    assert(fw_make_check_path(&p, path, sizeof(path)) == FW_OK);
    assert(strstr(path, "sequence=0&security=0"));
    p.channel = "beta+test";
    p.current_version = "1.0.0+build";
    assert(fw_make_check_path(&p, path, sizeof(path)) == FW_OK);
    assert(strstr(path, "channel=beta%2Btest"));
    assert(strstr(path, "version=1.0.0%2Bbuild"));
    p.channel = "stable";
    assert(fw_make_check_path(&p, path, 5) == FW_BUFFER_TOO_SMALL);
    assert(fw_heartbeat(&p, "1.0.0", "idle", transport, NULL) == FW_DISABLED);
    assert(calls == 0);
    p.telemetry_enabled = 1;
    assert(fw_heartbeat(&p, "1.0.0", "idle", transport, NULL) == FW_DISABLED);
    assert(calls == 0);
    p.hub_telemetry_enabled = 1;
    assert(fw_heartbeat(&p, "1.0.0", "idle", transport, NULL) == FW_OK);
    assert(calls == 1);
    p.telemetry_enabled = 0;
    assert(fw_heartbeat(&p, "1.0.0", "idle", transport, NULL) == FW_DISABLED);
    assert(calls == 1);
    puts("C SDK: Go/Ed25519 interoperability, tamper, compatibility, rollback, clock, digest, URL "
         "and telemetry opt-out: PASS");
    return 0;
}
