#include "fw485.h"
#include <assert.h>
#include <string.h>
static int writes = 0, activations = 0, allowed = 0;
static int authorize(void *ctx) {
    (void)ctx;
    return allowed;
}
static int begin(void *ctx, uint32_t size, const uint8_t hash[32], const char *version) {
    (void)ctx;
    (void)hash;
    assert(size == 4);
    assert(!strcmp(version, "1.1.0"));
    return 0;
}
static int write_image(void *ctx, uint32_t off, const uint8_t *data, size_t n) {
    (void)ctx;
    assert(off == 0 && n == 4 && !memcmp(data, "test", 4));
    writes++;
    return 0;
}
/* Test fixture injects a deterministic hash. Board ports must compute SHA-256
 * over actual staged flash; a constant or expected digest is never acceptable. */
static int hash_image(void *ctx, uint8_t out[32]) {
    (void)ctx;
    memset(out, 0xab, 32);
    return 0;
}
static int activate(void *ctx) {
    (void)ctx;
    activations++;
    return 0;
}
static int transact(fw485_receiver *r, fw485_packet *p, uint8_t *frame, size_t *n) {
    uint8_t reply[FW485_FRAME_MAX];
    fw485_packet ack;
    *n = fw485_encode(p, frame, FW485_FRAME_MAX);
    assert(*n);
    size_t len = fw485_handle(r, frame, *n, reply, sizeof(reply));
    assert(fw485_decode(reply, len, &ack));
    assert(ack.command == (p->command | 0x80));
    return ack.payload[0];
}
int main(void) {
    fw485_receiver r;
    fw485_ops ops = {0};
    uint8_t frame[FW485_FRAME_MAX], reply[FW485_FRAME_MAX], payload[166] = {0};
    size_t n;
    fw485_packet p = {1, FW485_HELLO, 0, 0, 0, 0};
    ops.capacity = 1024;
    ops.installed_version = "1.0.0";
    ops.hardware = "test-board";
    ops.authorized = authorize;
    ops.begin = begin;
    ops.write = write_image;
    ops.hash = hash_image;
    ops.activate = activate;
    assert(fw485_init(&r, 1, &ops));
    assert(transact(&r, &p, frame, &n) == FW485_OK);
    assert(writes == 0);
    payload[3] = 4;
    memset(payload + 4, 0xab, 32);
    memcpy(payload + 36, "1.1.0", 6);
    memcpy(payload + 101, "test-board", 11);
    p.command = FW485_BEGIN;
    p.session = 42;
    p.sequence = 1;
    p.payload = payload;
    p.length = 166;
    assert(transact(&r, &p, frame, &n) == FW485_DENIED);
    allowed = 1;
    assert(transact(&r, &p, frame, &n) == FW485_OK);
    memset(payload, 0, 4);
    memcpy(payload + 4, "test", 4);
    p.command = FW485_DATA;
    p.sequence = 2;
    p.length = 8;
    assert(transact(&r, &p, frame, &n) == FW485_OK);
    assert(transact(&r, &p, frame, &n) == FW485_OK);
    assert(writes == 1);
    frame[n - 1] ^= 1;
    assert(fw485_handle(&r, frame, n, reply, sizeof(reply)) == 0);
    frame[n - 1] ^= 1;
    payload[4] ^= 1;
    assert(transact(&r, &p, frame, &n) == FW485_WRONG_ORDER);
    payload[4] ^= 1;
    p.session = 99;
    assert(transact(&r, &p, frame, &n) == FW485_WRONG_SESSION);
    p.session = 42;
    p.command = FW485_VERIFY;
    p.sequence = 3;
    p.length = 0;
    assert(transact(&r, &p, frame, &n) == FW485_OK);
    p.command = FW485_COMMIT;
    p.sequence = 4;
    allowed = 0;
    assert(transact(&r, &p, frame, &n) == FW485_DENIED);
    allowed = 1;
    assert(transact(&r, &p, frame, &n) == FW485_OK);
    assert(activations == 0);
    assert(fw485_after_reply_transmitted(&r) == FW485_OK);
    assert(activations == 1);
    assert(fw485_after_reply_transmitted(&r) != FW485_OK);
    return 0;
}
