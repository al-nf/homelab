#include <stdio.h>
#include "platform.h"
#include "xil_printf.h"
#include "init.h"


int main()
{
    init_platform();
    init_emac();
    init_intc();
    
    print("Waiting for frames...\r\n");
    
    u32 netCtrl = XEmacPs_ReadReg(Emac.Config.BaseAddress, XEMACPS_NWCTRL_OFFSET);
    u32 netCfg = XEmacPs_ReadReg(Emac.Config.BaseAddress, XEMACPS_NWCFG_OFFSET);
    u32 rxStatus = XEmacPs_ReadReg(Emac.Config.BaseAddress, XEMACPS_RXSR_OFFSET);
    xil_printf("Network Control: 0x%08lx\r\n", netCtrl);
    xil_printf("Network Config: 0x%08lx\r\n", netCfg);
    xil_printf("RX Status: 0x%08lx\r\n", rxStatus);
    xil_printf("RX enabled: %s\r\n", (netCtrl & XEMACPS_NWCTRL_RXEN_MASK) ? "YES" : "NO");
    xil_printf("TX enabled: %s\r\n", (netCtrl & XEMACPS_NWCTRL_TXEN_MASK) ? "YES" : "NO");

    u32 count = 0;
    while (1) {
        if (count % 10000000 == 0) {
            print("."); // just to confirm polling is happening
        }
        count++;
    }
    cleanup_platform();
    return 0;
}
