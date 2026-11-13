/** @file boot.c
 * @brief Reference staging/copy bootloader, explicitly NOT hardware-qualified.
 * Power-loss recovery retries the complete copy from preserved staging while
 * metadata remains PENDING. Only verified active code is ever jumped to.
 * It cannot run the previous app during a failed copy (not A/B rollback).
 */
#include "port.h"
#include <stddef.h>
#include <string.h>
#include <tinycrypt/constants.h>
#include <tinycrypt/sha256.h>
#define META_MAGIC 0x46484D31u
#define META_PENDING 0xfffffffeu
#define META_INSTALLED 0xfffffffcu
typedef struct {
    uint32_t magic, size;
    uint8_t hash[32];
    char version[65], hardware[65];
    uint16_t crc;
    uint32_t state;
} metadata;
static metadata pending;
static int hash_region(uint32_t start, uint32_t size, uint8_t out[32]) {
    struct tc_sha256_state_struct s;
    return tc_sha256_init(&s) == TC_CRYPTO_SUCCESS &&
                   tc_sha256_update(&s, (const uint8_t *)start, size) == TC_CRYPTO_SUCCESS &&
                   tc_sha256_final(out, &s) == TC_CRYPTO_SUCCESS
               ? 0
               : -1;
}
static int same_hash(uint32_t start, const metadata *m) {
    uint8_t actual[32];
    return hash_region(start, m->size, actual) == 0 && memcmp(actual, m->hash, 32) == 0;
}
static int metadata_valid(const metadata *m) {
    return m->magic == META_MAGIC && m->size > 0x800u && m->size <= FH_IMAGE_MAX &&
           fw485_crc16((const uint8_t *)m, offsetof(metadata, crc)) == m->crc &&
           memchr(m->version, 0, 65) && memchr(m->hardware, 0, 65) &&
           !strcmp(m->hardware, FH_HARDWARE) &&
           (m->state == META_PENDING || m->state == META_INSTALLED);
}
static int staged_info_valid(const metadata *m) {
    const fh_app_info *p = (const fh_app_info *)(FH_STAGE_BASE + FH_INFO_OFFSET);
    return p->magic == FH_INFO_MAGIC && memchr(p->version, 0, 65) && memchr(p->hardware, 0, 65) &&
           !strcmp(p->version, m->version) && !strcmp(p->hardware, m->hardware);
}
static int authorized(void *ctx) {
    (void)ctx;
    return 1;
} /* Only running in explicitly entered bootloader mode. Physical RS485 bus is trusted. */
static int begin(void *ctx, uint32_t size, const uint8_t sha[32], const char *version) {
    uint32_t p;
    (void)ctx;
    if (size <= 0x800u || size > FH_IMAGE_MAX)
        return -1;
    memset(&pending, 0, sizeof(pending));
    pending.magic = META_MAGIC;
    pending.size = size;
    memcpy(pending.hash, sha, 32);
    strcpy(pending.version, version);
    strcpy(pending.hardware, FH_HARDWARE);
    pending.crc = fw485_crc16((const uint8_t *)&pending, offsetof(metadata, crc));
    pending.state = META_PENDING;
    for (p = 0; p < size; p += FH_PAGE_SIZE)
        if (fh_erase_page(FH_STAGE_BASE + p) != 0)
            return -1;
    return 0;
}
static int write_stage(void *ctx, uint32_t offset, const uint8_t *data, size_t length) {
    (void)ctx;
    if (offset % 4u || offset > pending.size || length > pending.size - offset)
        return -1;
    return fh_program(FH_STAGE_BASE + offset, data, (uint32_t)length);
}
static int staged_hash(void *ctx, uint8_t actual[32]) {
    (void)ctx;
    return hash_region(FH_STAGE_BASE, pending.size, actual);
}
static int activate(void *ctx) {
    (void)ctx;
    if (!staged_info_valid(&pending) || !fh_vector_valid(FH_STAGE_BASE, pending.size) ||
        !same_hash(FH_STAGE_BASE, &pending))
        return -1;
    if (fh_erase_page(FH_META_ADDR) != 0 ||
        fh_program(FH_META_ADDR, &pending, offsetof(metadata, state)) != 0 ||
        fh_program(FH_META_ADDR + offsetof(metadata, state), &pending.state, 4) != 0)
        return -1;
    __NVIC_SystemReset();
    return 0;
}
static int copy_pending(const metadata *m) {
    uint32_t p, state = META_INSTALLED;
    if (!staged_info_valid(m) || !fh_vector_valid(FH_STAGE_BASE, m->size) ||
        !same_hash(FH_STAGE_BASE, m))
        return -1;
    for (p = 0; p < m->size; p += FH_PAGE_SIZE) {
        uint32_t size = m->size - p > FH_PAGE_SIZE ? FH_PAGE_SIZE : m->size - p;
        if (fh_erase_page(FH_APP_BASE + p) != 0 ||
            fh_program(FH_APP_BASE + p, (const void *)(FH_STAGE_BASE + p), size) != 0)
            return -1;
    }
    if (!same_hash(FH_APP_BASE, m))
        return -1;
    return fh_program(FH_META_ADDR + offsetof(metadata, state), &state, 4);
}
#ifndef FH_BOOT_HOST_TEST
// clang-format off
__asm static void branch_to_app(uint32_t sp,uint32_t pc){
    MSR MSP,R0
    BX R1
}
// clang-format on
static void jump(void) {
    uint32_t i;
    __disable_irq();
    SysTick->CTRL = 0;
    SysTick->LOAD = 0;
    SysTick->VAL = 0;
    for (i = 0; i < 8; i++) {
        NVIC->ICER[i] = 0xffffffffu;
        NVIC->ICPR[i] = 0xffffffffu;
    }
    SCB->VTOR = FH_APP_BASE;
    __DSB();
    __ISB();
    branch_to_app(*(uint32_t *)FH_APP_BASE, *(uint32_t *)(FH_APP_BASE + 4));
}
int main(void) {
    static fw485_receiver receiver;
    const metadata *m = (const metadata *)FH_META_ADDR;
    const fh_app_info *info;
    fw485_ops ops;
    uint8_t frame[FW485_FRAME_MAX], reply[FW485_FRAME_MAX];
    size_t n, out;
    int requested = *(const uint32_t *)FH_REQUEST_ADDR == FH_BOOT_REQUEST;
    fh_port_init(0x08000000u);
    if (metadata_valid(m) && m->state == META_PENDING) {
        if (copy_pending(m) != 0)
            requested = 1;
    }
    if (requested) {
        (void)fh_erase_page(FH_REQUEST_ADDR);
    } else if (fh_running_info() && fh_vector_valid(FH_APP_BASE, FH_IMAGE_MAX) &&
               (!metadata_valid(m) || (m->state == META_INSTALLED && same_hash(FH_APP_BASE, m))))
        jump();
    memset(&ops, 0, sizeof(ops));
    info = fh_running_info();
    ops.capacity = FH_IMAGE_MAX;
    ops.hardware = FH_HARDWARE;
    ops.installed_version = metadata_valid(m) && m->state != META_INSTALLED ? "recovery" : info ? info->version : "0.0.0";
    ops.authorized = authorized;
    ops.begin = begin;
    ops.write = write_stage;
    ops.hash = staged_hash;
    ops.activate = activate;
    if (!fw485_init(&receiver, 1, &ops))
        while (1) {
        }
    while (1) {
        n = fh_receive(frame, sizeof(frame));
        if (!n)
            continue;
        out = fw485_handle(&receiver, frame, n, reply, sizeof(reply));
        if (out && fh_send(reply, out) == 0 && receiver.state == FW485_COMMIT_PENDING)
            (void)fw485_after_reply_transmitted(&receiver);
    }
}
#endif
