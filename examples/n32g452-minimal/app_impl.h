/** Minimal application: reports version and accepts an explicit boot request.
 * No existing product business code, FreeRTOS, outputs or internet stack.
 */
#include "port.h"
#include <string.h>
#pragma arm section rodata = "FW_INFO"
const fh_app_info firmware_info = {FH_INFO_MAGIC, APP_VERSION, FH_HARDWARE};
#pragma arm section rodata
static int denied(void *ctx) {
    (void)ctx;
    return 0;
}
int main(void) {
    fw485_receiver receiver;
    fw485_ops ops;
    uint8_t frame[FW485_FRAME_MAX], reply[FW485_FRAME_MAX];
    size_t n, out;
    int reset;
    fh_port_init(FH_APP_BASE);
    memset(&ops, 0, sizeof(ops));
    ops.capacity = FH_IMAGE_MAX;
    ops.hardware = FH_HARDWARE;
    ops.installed_version = APP_VERSION;
    ops.authorized = denied;
    fw485_init(&receiver, 1, &ops);
    while (1) {
        n = fh_receive(frame, sizeof(frame));
        if (!n || frame[0] != 1)
            continue;
        out = 0;
        reset = 0;
        if (frame[1] == 3 && n == 8) {
            uint16_t reg = (uint16_t)frame[2] * 256 + frame[3],
                     count = (uint16_t)frame[4] * 256 + frame[5];
            if (reg == 17 && count == 2) {
                reply[0] = 1;
                reply[1] = 3;
                reply[2] = 4;
                reply[3] = (uint8_t)(APP_VERSION_NUMBER >> 8);
                reply[4] = (uint8_t)APP_VERSION_NUMBER;
                reply[5] = 0;
                reply[6] = 1;
                out = 7;
            }
        } else if (frame[1] == 6 && n == 8 && frame[2] == 0 && frame[3] == 33 && frame[4] == 0xb0 &&
                   frame[5] == 7) {
            uint32_t marker = FH_BOOT_REQUEST;
            if (fh_erase_page(FH_REQUEST_ADDR) == 0 &&
                fh_program(FH_REQUEST_ADDR, &marker, 4) == 0) {
                memcpy(reply, frame, 6);
                out = 6;
                reset = 1;
            }
        } else if (frame[1] == 0x41) {
            out = fw485_handle(&receiver, frame, n, reply, sizeof(reply));
            if (out)
                (void)fh_send(reply, out);
            continue;
        }
        if (out) {
            uint16_t crc = fw485_crc16(reply, out);
            reply[out++] = (uint8_t)crc;
            reply[out++] = (uint8_t)(crc >> 8);
            if (fh_send(reply, out) == 0 && reset)
                __NVIC_SystemReset();
        }
    }
}
