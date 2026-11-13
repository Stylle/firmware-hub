#include "port.h"
#include <string.h>
uint32_t SystemCoreClock = 8000000u;
volatile uint32_t fh_ticks;
// Startup uses only HSI, independent of an unknown external oscillator.
void SystemInit(void) {
    SCB->CPACR |= (3UL << 20) | (3UL << 22);
    RCC_DeInit();
    RCC_EnableHsi(ENABLE);
    SystemCoreClock = 8000000u;
}
void SystemCoreClockUpdate(void) {
    SystemCoreClock = 8000000u;
}
void SysTick_Handler(void) {
    fh_ticks++;
}
void fh_port_init(uint32_t vector_base) {
    GPIO_InitType gpio;
    USART_InitType uart;
    SCB->VTOR = vector_base;
    __DSB();
    __ISB();
    RCC_EnableAPB2PeriphClk(RCC_APB2_PERIPH_GPIOA | RCC_APB2_PERIPH_USART1 | RCC_APB2_PERIPH_AFIO,
                            ENABLE);
    GPIO_InitStruct(&gpio);
    gpio.Pin = GPIO_PIN_9;
    gpio.GPIO_Mode = GPIO_Mode_AF_PP;
    gpio.GPIO_Speed = GPIO_Speed_2MHz;
    GPIO_InitPeripheral(GPIOA, &gpio);
    gpio.Pin = GPIO_PIN_10;
    gpio.GPIO_Mode = GPIO_Mode_IN_FLOATING;
    GPIO_InitPeripheral(GPIOA, &gpio);
    USART_StructInit(&uart);
    uart.BaudRate = 9600;
    uart.WordLength = USART_WL_8B;
    uart.StopBits = USART_STPB_1;
    uart.Parity = USART_PE_NO;
    uart.HardwareFlowControl = USART_HFCTRL_NONE;
    uart.Mode = USART_MODE_RX | USART_MODE_TX;
    USART_Init(USART1, &uart);
    USART_Enable(USART1, ENABLE);
    fh_ticks = 0;
    SysTick_Config(8000u);
    __enable_irq();
}
size_t fh_receive(uint8_t *frame, size_t cap) {
    static uint8_t buf[FW485_FRAME_MAX];
    static size_t used;
    static uint32_t last;
    uint32_t status = USART1->STS;
    size_t expected = 0;
    if (used && (uint32_t)(fh_ticks - last) > 30u)
        used = 0;
    if (status & (USART_FLAG_OREF | USART_FLAG_NEF | USART_FLAG_FEF | USART_FLAG_PEF)) {
        volatile uint32_t discarded = USART1->DAT;
        (void)discarded;
        used = 0;
        return 0;
    }
    if (!(status & USART_FLAG_RXDNE))
        return 0;
    if (used >= sizeof(buf)) {
        used = 0;
        return 0;
    }
    buf[used++] = (uint8_t)USART1->DAT;
    last = fh_ticks;
    if (used >= 2) {
        if (buf[1] == 3 || buf[1] == 6)
            expected = 8;
        else if (buf[1] == 0x41 && used >= 4) {
            expected = 15u + (size_t)buf[2] * 256u + buf[3];
            if (expected > sizeof(buf)) {
                used = 0;
                return 0;
            }
        }
    }
    if (expected && used == expected) {
        used = 0;
        if (expected > cap || fw485_crc16(buf, expected) != 0)
            return 0;
        memcpy(frame, buf, expected);
        return expected;
    }
    return 0;
}
int fh_send(const uint8_t *data, size_t n) {
    size_t i;
    uint32_t start = fh_ticks;
    for (i = 0; i < n; i++) {
        while (USART_GetFlagStatus(USART1, USART_FLAG_TXDE) == RESET) {
            if ((uint32_t)(fh_ticks - start) > 1000u)
                return -1;
        }
        USART_SendData(USART1, data[i]);
    }
    while (USART_GetFlagStatus(USART1, USART_FLAG_TXC) == RESET) {
        if ((uint32_t)(fh_ticks - start) > 1000u)
            return -1;
    }
    return 0;
}
int fh_erase_page(uint32_t address) {
    FLASH_STS result;
    if (address < FH_APP_BASE || address > FH_META_ADDR || address % FH_PAGE_SIZE)
        return -1;
    FLASH_Unlock();
    FLASH_ClearFlag(FLASH_STS_CLRFLAG);
    result = FLASH_EraseOnePage(address);
    FLASH_Lock();
    return result == FLASH_COMPL ? 0 : -1;
}
int fh_program(uint32_t address, const void *data, uint32_t size) {
    uint32_t offset, word;
    const uint8_t *bytes = (const uint8_t *)data;
    FLASH_STS result = FLASH_COMPL;
    if (address < FH_APP_BASE || address % 4u || size > 0x08080000u - address ||
        address >= 0x08080000u)
        return -1;
    FLASH_Unlock();
    for (offset = 0; offset < size; offset += 4) {
        word = 0xffffffffu;
        memcpy(&word, bytes + offset, size - offset >= 4 ? 4 : size - offset);
        FLASH_ClearFlag(FLASH_STS_CLRFLAG);
        result = FLASH_ProgramWord(address + offset, word);
        if (result != FLASH_COMPL)
            break;
    }
    FLASH_Lock();
    return result == FLASH_COMPL ? 0 : -1;
}
int fh_vector_valid(uint32_t base, uint32_t size) {
    uint32_t sp = *(const uint32_t *)base, pc = *(const uint32_t *)(base + 4);
    return size > 0x800u && size <= FH_IMAGE_MAX && sp > 0x20000000u && sp <= 0x20024000u &&
           (sp & 7u) == 0 && (pc & 1u) && ((pc & ~1u) >= FH_APP_BASE) &&
           ((pc & ~1u) < FH_APP_BASE + size);
}
const fh_app_info *fh_running_info(void) {
    const fh_app_info *p = (const fh_app_info *)(FH_APP_BASE + FH_INFO_OFFSET);
    return p->magic == FH_INFO_MAGIC && memchr(p->version, 0, 65) && memchr(p->hardware, 0, 65) ? p
                                                                                                : 0;
}
