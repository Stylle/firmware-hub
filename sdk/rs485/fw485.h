/** @file fw485.h
 * @brief FH485/1 reference transport for a NEW, explicitly integrated bootloader.
 * @warning Not compatible with arbitrary existing Modbus/IAP bootloaders.
 * This protocol supplies framing, ordering and integrity, NOT authentication or
 * a flash driver. BEGIN/COMMIT require an application-owned maintenance gate.
 * Active firmware must never be erased by the staging callbacks. A board port
 * must implement a power-loss-safe boot journal/copy or A/B installation first.
 */
#ifndef FW485_H
#define FW485_H
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
#define FW485_PAYLOAD_MAX 192u
#define FW485_FRAME_MAX (15u + FW485_PAYLOAD_MAX)
#define FW485_NAME_CAP 65u
enum {
    FW485_HELLO = 1,
    FW485_BEGIN = 2,
    FW485_DATA = 3,
    FW485_VERIFY = 4,
    FW485_COMMIT = 5,
    FW485_ABORT = 6
};
enum { FW485_IDLE = 0, FW485_RECEIVING = 1, FW485_VERIFIED = 2, FW485_COMMIT_PENDING = 3 };
enum {
    FW485_OK = 0,
    FW485_BAD_REQUEST = 1,
    FW485_DENIED = 2,
    FW485_WRONG_SESSION = 3,
    FW485_WRONG_ORDER = 4,
    FW485_STORAGE_ERROR = 5,
    FW485_HASH_ERROR = 6,
    FW485_UNSUPPORTED = 7
};
typedef struct {
    uint8_t address, command;
    uint32_t session, sequence;
    const uint8_t *payload;
    size_t length;
} fw485_packet;
typedef struct {
    void *ctx;
    uint32_t capacity;
    const char *installed_version, *hardware;
    /** Return nonzero only while a LOCAL maintenance authorization is active. */
    int (*authorized)(void *ctx);
    /** Prepare a SEPARATE staging slot; zero on success. Never erase active code. */
    int (*begin)(void *ctx, uint32_t size, const uint8_t sha256[32], const char *version);
    /** Sequential staging write. Zero on success; called once for each new block. */
    int (*write)(void *ctx, uint32_t offset, const uint8_t *data, size_t length);
    /** Independently hash the complete staged image into actual_sha256. */
    int (*hash)(void *ctx, uint8_t actual_sha256[32]);
    /** Atomically schedule the already verified image for safe boot installation.
     * Called only by fw485_after_reply_transmitted(), after the UART TC flag.
     * Must remain recoverable through power loss; zero on accepted activation. */
    int (*activate)(void *ctx);
    /** Discard staging state only; never erase currently bootable firmware. */
    void (*abort)(void *ctx);
} fw485_ops;
typedef struct {
    fw485_ops ops;
    uint8_t address, state;
    uint32_t session, last_sequence, received, total;
    uint8_t expected_sha256[32], last_request[FW485_FRAME_MAX], last_reply[FW485_FRAME_MAX];
    size_t last_request_size, last_reply_size;
} fw485_receiver;
/** Standard Modbus CRC16, low byte first on wire. */
uint16_t fw485_crc16(const uint8_t *data, size_t length);
/** Encode/decode the bounded FH485 frame. Zero indicates invalid framing. */
size_t fw485_encode(const fw485_packet *packet, uint8_t *out, size_t capacity);
int fw485_decode(const uint8_t *frame, size_t length, fw485_packet *out);
/** Initialize with a unicast address 1..247. No flash operation is performed. */
int fw485_init(fw485_receiver *receiver, uint8_t address, const fw485_ops *ops);
/** Handle ONE complete frame; zero response length means drop/no side effect.
 * Retransmitting an identical last request returns the cached ACK without
 * another staging write. A different request with the same sequence is refused.
 */
size_t fw485_handle(fw485_receiver *receiver, const uint8_t *request, size_t length, uint8_t *reply,
                    size_t capacity);
/** Caller invokes only after the entire COMMIT ACK has physically left UART.
 * Does nothing unless a verified, locally authorized commit is pending.
 * Installed version must be reported only after real successful boot.
 */
int fw485_after_reply_transmitted(fw485_receiver *receiver);
#ifdef __cplusplus
}
#endif
#endif
