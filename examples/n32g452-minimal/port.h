/** @file port.h
 * @brief Minimal N32G452 512-KiB reference board contract. NOT a universal BSP.
 * USART1 PA9/PA10, 9600 8N1, address 1. Requires an automatic-direction RS485
 * transceiver. No laser, motor, relay, or unknown board output is enabled.
 * Review flash capacity/pins and use a recoverable bench board before flashing.
 */
#ifndef FH_N32_PORT_H
#define FH_N32_PORT_H
#include <stdint.h>
#ifndef FH_BOOT_HOST_TEST
#include "n32g45x.h"
#else
void __NVIC_SystemReset(void);
#endif
#include "fw485.h"
#define FH_APP_BASE 0x08005000u
#define FH_STAGE_BASE 0x08040000u
#define FH_IMAGE_MAX (FH_STAGE_BASE - FH_APP_BASE)
#define FH_REQUEST_ADDR 0x0807F000u
#define FH_META_ADDR 0x0807F800u
#define FH_PAGE_SIZE 0x800u
#define FH_INFO_OFFSET 0x400u
#define FH_INFO_MAGIC 0x46484150u
#define FH_BOOT_REQUEST 0xB0071234u
#define FH_HARDWARE "n32g452-demo"
typedef struct {
    uint32_t magic;
    char version[65];
    char hardware[65];
} fh_app_info;
extern volatile uint32_t fh_ticks;
void fh_port_init(uint32_t vector_base);
size_t fh_receive(uint8_t *frame, size_t capacity);
int fh_send(const uint8_t *frame, size_t length);
int fh_erase_page(uint32_t address);
int fh_program(uint32_t address, const void *data, uint32_t size);
int fh_vector_valid(uint32_t base, uint32_t size);
const fh_app_info *fh_running_info(void);
#endif
