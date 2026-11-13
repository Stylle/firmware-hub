#include "fw485.h"
#include <string.h>
static uint32_t be32(const uint8_t *p) {
    return (uint32_t)p[0] << 24 | (uint32_t)p[1] << 16 | (uint32_t)p[2] << 8 | p[3];
}
static void put32(uint8_t *p, uint32_t v) {
    p[0] = (uint8_t)(v >> 24);
    p[1] = (uint8_t)(v >> 16);
    p[2] = (uint8_t)(v >> 8);
    p[3] = (uint8_t)v;
}
static int name_valid(const char *s) {
    size_t i;
    if (!s)
        return 0;
    for (i = 0; i < FW485_NAME_CAP; i++) {
        char c = s[i];
        if (!c)
            return i > 0;
        if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
              (i > 0 && (c == '.' || c == '-' || c == '_' || c == '+'))))
            return 0;
    }
    return 0;
}
uint16_t fw485_crc16(const uint8_t *p, size_t n) {
    uint16_t crc = 0xffff;
    size_t i;
    unsigned b;
    if (!p && n)
        return 0;
    for (i = 0; i < n; i++) {
        crc ^= p[i];
        for (b = 0; b < 8; b++)
            crc = (uint16_t)((crc >> 1) ^ ((crc & 1) ? 0xa001 : 0));
    }
    return crc;
}
size_t fw485_encode(const fw485_packet *p, uint8_t *out, size_t cap) {
    uint16_t crc;
    size_t n;
    if (!p || !out || p->address < 1 || p->address > 247 || p->length > FW485_PAYLOAD_MAX ||
        (!p->payload && p->length))
        return 0;
    n = 15 + p->length;
    if (cap < n)
        return 0;
    out[0] = p->address;
    out[1] = 0x41;
    out[2] = (uint8_t)(p->length >> 8);
    out[3] = (uint8_t)p->length;
    out[4] = p->command;
    put32(out + 5, p->session);
    put32(out + 9, p->sequence);
    if (p->length)
        memcpy(out + 13, p->payload, p->length);
    crc = fw485_crc16(out, n - 2);
    out[n - 2] = (uint8_t)crc;
    out[n - 1] = (uint8_t)(crc >> 8);
    return n;
}
int fw485_decode(const uint8_t *f, size_t n, fw485_packet *p) {
    size_t len;
    if (!f || !p || n < 15 || n > FW485_FRAME_MAX || f[0] < 1 || f[0] > 247 || f[1] != 0x41 ||
        fw485_crc16(f, n) != 0)
        return 0;
    len = (size_t)f[2] * 256 + f[3];
    if (len > FW485_PAYLOAD_MAX || n != len + 15)
        return 0;
    p->address = f[0];
    p->command = f[4];
    p->session = be32(f + 5);
    p->sequence = be32(f + 9);
    p->payload = f + 13;
    p->length = len;
    return 1;
}
int fw485_init(fw485_receiver *r, uint8_t address, const fw485_ops *ops) {
    if (!r || !ops || address < 1 || address > 247 || !ops->capacity ||
        !name_valid(ops->installed_version) || !name_valid(ops->hardware))
        return 0;
    memset(r, 0, sizeof(*r));
    r->ops = *ops;
    r->address = address;
    return 1;
}
size_t fw485_handle(fw485_receiver *r, const uint8_t *request, size_t n, uint8_t *reply,
                    size_t capacity) {
    fw485_packet p, response;
    uint8_t out[140], actual[32];
    uint8_t code = FW485_OK;
    size_t length = 10, written;
    unsigned diff = 0;
    size_t i;
    if (!r || !reply || capacity < FW485_FRAME_MAX || !fw485_decode(request, n, &p) ||
        p.address != r->address || (p.command & 0x80))
        return 0;
    if (p.command == FW485_HELLO) {
        if (p.length)
            code = FW485_BAD_REQUEST;
        else {
            length = 140;
            memset(out + 10, 0, 130);
            memcpy(out + 10, r->ops.installed_version, strlen(r->ops.installed_version));
            memcpy(out + 75, r->ops.hardware, strlen(r->ops.hardware));
        }
    } else if (!p.session || !p.sequence)
        code = FW485_BAD_REQUEST;
    else if (r->last_request_size && p.session == r->session && p.sequence == r->last_sequence) {
        if (n == r->last_request_size && !memcmp(request, r->last_request, n)) {
            memcpy(reply, r->last_reply, r->last_reply_size);
            return r->last_reply_size;
        }
        code = FW485_WRONG_ORDER;
    } else if (p.command == FW485_BEGIN) {
        if (r->state != FW485_IDLE)
            code = FW485_WRONG_SESSION;
        else if (!r->ops.authorized || !r->ops.authorized(r->ops.ctx))
            code = FW485_DENIED;
        else if (!r->ops.begin || !r->ops.write || !r->ops.hash || !r->ops.activate)
            code = FW485_UNSUPPORTED;
        else if (p.length != 166 || !name_valid((const char *)p.payload + 36) ||
                 !name_valid((const char *)p.payload + 101) ||
                 strcmp((const char *)p.payload + 101, r->ops.hardware) || be32(p.payload) == 0 ||
                 be32(p.payload) > r->ops.capacity)
            code = FW485_BAD_REQUEST;
        else if (r->ops.begin(r->ops.ctx, be32(p.payload), p.payload + 4,
                              (const char *)p.payload + 36) != 0)
            code = FW485_STORAGE_ERROR;
        else {
            r->session = p.session;
            r->received = 0;
            r->total = be32(p.payload);
            memcpy(r->expected_sha256, p.payload + 4, 32);
            r->state = FW485_RECEIVING;
            r->last_request_size = 0;
        }
    } else if (p.session != r->session)
        code = FW485_WRONG_SESSION;
    else if (r->last_sequence == UINT32_MAX || p.sequence != r->last_sequence + 1)
        code = FW485_WRONG_ORDER;
    else if (p.command == FW485_DATA) {
        if (r->state != FW485_RECEIVING || p.length <= 4 || be32(p.payload) != r->received ||
            p.length - 4 > r->total - r->received)
            code = FW485_BAD_REQUEST;
        else if (r->ops.write(r->ops.ctx, r->received, p.payload + 4, p.length - 4) != 0)
            code = FW485_STORAGE_ERROR;
        else
            r->received += (uint32_t)(p.length - 4);
    } else if (p.command == FW485_VERIFY) {
        if (p.length || r->state != FW485_RECEIVING || r->received != r->total)
            code = FW485_BAD_REQUEST;
        else if (r->ops.hash(r->ops.ctx, actual) != 0)
            code = FW485_STORAGE_ERROR;
        else {
            for (i = 0; i < 32; i++)
                diff |= actual[i] ^ r->expected_sha256[i];
            if (diff)
                code = FW485_HASH_ERROR;
            else
                r->state = FW485_VERIFIED;
        }
    } else if (p.command == FW485_COMMIT) {
        if (p.length || r->state != FW485_VERIFIED)
            code = FW485_BAD_REQUEST;
        else if (!r->ops.authorized || !r->ops.authorized(r->ops.ctx))
            code = FW485_DENIED;
        else
            r->state = FW485_COMMIT_PENDING;
    } else if (p.command == FW485_ABORT) {
        if (p.length)
            code = FW485_BAD_REQUEST;
        else {
            if (r->ops.abort)
                r->ops.abort(r->ops.ctx);
            r->state = FW485_IDLE;
            r->received = 0;
        }
    } else
        code = FW485_UNSUPPORTED;
    out[0] = code;
    put32(out + 1, r->received);
    out[5] = r->state;
    put32(out + 6, r->ops.capacity);
    response = p;
    response.command = (uint8_t)(p.command | 0x80);
    response.payload = out;
    response.length = length;
    written = fw485_encode(&response, reply, capacity);
    if (code == FW485_OK && p.command != FW485_HELLO) {
        r->last_sequence = p.sequence;
        memcpy(r->last_request, request, n);
        r->last_request_size = n;
        memcpy(r->last_reply, reply, written);
        r->last_reply_size = written;
    }
    return written;
}
int fw485_after_reply_transmitted(fw485_receiver *r) {
    if (!r || r->state != FW485_COMMIT_PENDING)
        return FW485_BAD_REQUEST;
    if (!r->ops.authorized || !r->ops.authorized(r->ops.ctx))
        return FW485_DENIED;
    if (!r->ops.activate || r->ops.activate(r->ops.ctx) != 0)
        return FW485_STORAGE_ERROR;
    r->state = FW485_IDLE;
    return FW485_OK;
}
