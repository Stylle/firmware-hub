/* Host simulation of the ACTUAL reference copy/journal code, not a hardware
 * power-cut qualification. No serial port or real flash controller is used. */
#define _GNU_SOURCE
#define FH_BOOT_HOST_TEST
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#ifdef _WIN32
#include <windows.h>
#else
#include <sys/mman.h>
#endif
#include "../examples/n32g452-minimal/boot.c"
static int failure_after = -1, reset_count;
void __NVIC_SystemReset(void) {
    reset_count++;
}
int fh_erase_page(uint32_t address) {
    if (failure_after == 0)
        return -1;
    if (failure_after > 0)
        failure_after--;
    assert(address >= FH_APP_BASE && address <= FH_META_ADDR && address % FH_PAGE_SIZE == 0);
    memset((void *)(uintptr_t)address, 0xff, FH_PAGE_SIZE);
    return 0;
}
int fh_program(uint32_t address, const void *data, uint32_t size) {
    uint32_t i;
    const uint8_t *p = data;
    uint8_t *out = (uint8_t *)(uintptr_t)address;
    if (failure_after == 0)
        return -1;
    if (failure_after > 0)
        failure_after--;
    assert(address >= FH_APP_BASE && address + size <= 0x08080000u);
    for (i = 0; i < size; i++) {
        assert((out[i] & p[i]) == p[i]);
        out[i] = p[i];
    }
    return 0;
}
int fh_vector_valid(uint32_t base, uint32_t size) {
    const uint32_t *v = (const uint32_t *)(uintptr_t)base;
    return size > 0x800 && size <= FH_IMAGE_MAX && v[0] == 0x20024000u &&
           v[1] == FH_APP_BASE + 0x801u;
}
int main(void) {
    void *flash;
#ifdef _WIN32
    flash = VirtualAlloc((void *)0x08000000u, 0x80000, MEM_COMMIT | MEM_RESERVE, PAGE_READWRITE);
#else
    flash = mmap((void *)0x08000000u, 0x80000, PROT_READ | PROT_WRITE,
                 MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0);
#endif
    assert(flash == (void *)0x08000000u);
    uint8_t image[6500], expected[32], actual[32];
    uint32_t vectors[2] = {0x20024000u, FH_APP_BASE + 0x801u};
    fh_app_info info = {FH_INFO_MAGIC, "1.1.0", FH_HARDWARE};
    struct tc_sha256_state_struct sha;
    int cut;
    assert(tc_sha256_init(&sha));
    assert(tc_sha256_update(&sha, (const uint8_t *)"abc", 3));
    assert(tc_sha256_final(actual, &sha));
    const uint8_t abc[32] = {0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea, 0x41, 0x41, 0x40,
                             0xde, 0x5d, 0xae, 0x22, 0x23, 0xb0, 0x03, 0x61, 0xa3, 0x96, 0x17,
                             0x7a, 0x9c, 0xb4, 0x10, 0xff, 0x61, 0xf2, 0x00, 0x15, 0xad};
    assert(!memcmp(actual, abc, 32));
    memset(image, 0xa5, sizeof(image));
    memcpy(image, vectors, sizeof(vectors));
    memcpy(image + FH_INFO_OFFSET, &info, sizeof(info));
    assert(tc_sha256_init(&sha));
    assert(tc_sha256_update(&sha, image, sizeof(image)));
    assert(tc_sha256_final(expected, &sha));
    assert(authorized(0));
    for (cut = 0; cut < 9; cut++) {
        memset(flash, 0xff, 0x80000);
        memset((void *)FH_APP_BASE, 0x11, sizeof(image));
        failure_after = -1;
        reset_count = 0;
        assert(begin(0, sizeof(image), expected, "1.1.0") == 0);
        assert(write_stage(0, 0, image, sizeof(image)) == 0);
        assert(staged_hash(0, actual) == 0 && !memcmp(actual, expected, 32));
        assert(activate(0) == 0 && reset_count == 1);
        const metadata *m = (const metadata *)FH_META_ADDR;
        assert(metadata_valid(m) && m->state == META_PENDING);
        failure_after = cut;
        assert(copy_pending(m) != 0);
        assert(m->state == META_PENDING);
        failure_after = -1;
        assert(copy_pending(m) == 0);
        assert(m->state == META_INSTALLED && same_hash(FH_APP_BASE, m));
        assert(!memcmp((void *)FH_APP_BASE, image, sizeof(image)));
    }
    ((uint8_t *)FH_STAGE_BASE)[100] ^= 1;
    assert(copy_pending((const metadata *)FH_META_ADDR) != 0);
    puts("N32 reference: SHA-256 known vector, staged hash, activation, 9 simulated copy "
         "interruption/recovery points and corruption rejection PASS");
#ifdef _WIN32
    VirtualFree(flash, 0, MEM_RELEASE);
#else
    munmap(flash, 0x80000);
#endif
    return 0;
}
