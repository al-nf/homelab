#include <stdio.h>
#include "platform.h"
#include "xil_printf.h"
#include "init.h"


int main()
{
    init_platform();
    init_emac();
    init_intc();

    while (1) {
        asm volatile("wfi");
    }
    cleanup_platform();
    return 0;
}
