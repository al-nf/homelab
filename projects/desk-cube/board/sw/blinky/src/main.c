/******************************************************************************
* Copyright (C) 2023 Advanced Micro Devices, Inc. All Rights Reserved.
* SPDX-License-Identifier: MIT
******************************************************************************/
/*
 * helloworld.c: simple test application
 *
 * This application configures UART 16550 to baud rate 9600.
 * PS7 UART (Zynq) is not initialized by this application, since
 * bootrom/bsp configures it to baud rate 115200
 *
 * ------------------------------------------------
 * | UART TYPE   BAUD RATE                        |
 * ------------------------------------------------
 *   uartns550   9600
 *   uartlite    Configurable only in HW design
 *   ps7_uart    115200 (configured by bootrom/bsp)
 */

#include <stdio.h>
#include <xemacps_bd.h>
#include <xemacps_bdring.h>
#include <xemacps_hw.h>
#include <xil_exception.h>
#include <xil_types.h>
#include <xparameters.h>
#include "platform.h"
#include "xil_printf.h"
#include "xscugic.h"
#include "xemacps.h"
#include "xdmaps.h"
#include "xil_mmu.h"
#include "xil_cache.h"

XScuGic intc;
XEmacPs emac;

/*
void myassertcb(const char8 *File, s32 Line) {
    xil_printf("assert in %s, line %d\r\n", File, Line);
}
*/

int init() {
    // FOR DEBUGGING
    // Xil_AssertSetCallback(myassertcb);

    // INTERRUPTS
    int Status;
    XScuGic_Config* intc_cfg = XScuGic_LookupConfig(XPAR_XSCUGIC_0_DEVICE_ID);

    Status = XScuGic_CfgInitialize(&intc, intc_cfg, intc_cfg->CpuBaseAddress);
    if (Status != XST_SUCCESS) {
        xil_printf("broke: gic init\n\r");
        return XST_FAILURE;
    }
    
    Status = XScuGic_SelfTest(&intc);
    if (Status != XST_SUCCESS) {
        xil_printf("broke: self test\n\r");
        return XST_FAILURE;
    }
    
    Xil_ExceptionRegisterHandler(
        XIL_EXCEPTION_ID_INT,
        (Xil_ExceptionHandler)XScuGic_InterruptHandler,
        &intc
    );
    
    Xil_ExceptionEnable();
    
    // EMAC
    XEmacPs_Config* emac_cfg = XEmacPs_LookupConfig(XPAR_XEMACPS_0_DEVICE_ID);

    Status = XEmacPs_CfgInitialize(&emac, emac_cfg, emac_cfg->BaseAddress);
    if (Status != XST_SUCCESS) {
        xil_printf("broke: emac init\n\r");
        return XST_FAILURE;
    }

    u8 mac[6] = {0x00, 0x18, 0x3e, 0x04, 0xec, 0xf9};
    XEmacPs_SetMacAddress(&emac, &mac, 1);

    XEmacPs_SetMdioDivisor(&emac, MDC_DIV_224);

    u16 phy;
    do {
        XEmacPs_PhyRead(
            &emac,
            1,
            1,
            &phy
        );
    } while (!(phy & 0x4));

    XEmacPs_SetOperatingSpeed(&emac, 1000);

    XEmacPs_SetOptions(&emac, XEMACPS_RECEIVER_ENABLE_OPTION);
    
    // BDRINGS
    Status = XEmacPs_BdRingCreate(
        &(XEmacPs_GetRxRing(&emac)),
        emac.RxBdRing.BaseBdAddr,
        emac.RxBdRing.BaseBdAddr,
        XEMACPS_BD_ALIGNMENT,
        XEMACPS_MAX_RXBD
    );
    if (Status != XST_SUCCESS) {
        xil_printf("broke: bdring (rx) create\n\r");
        return XST_FAILURE;
    }
    
    Status = XEmacPs_BdRingCreate(
        &(XEmacPs_GetTxRing(&emac)),
        emac.TxBdRing.BaseBdAddr,
        emac.TxBdRing.BaseBdAddr,
        XEMACPS_BD_ALIGNMENT,
        XEMACPS_MAX_TXBD
    );
    if (Status != XST_SUCCESS) {
        xil_printf("broke: bdring (tx) create\n\r");
        return XST_FAILURE;
    }

    XEmacPs_Bd bdtemplate;
    XEmacPs_BdClear(&bdtemplate);

    Status = XEmacPs_BdRingClone(
        &(XEmacPs_GetRxRing(&emac)),
        &bdtemplate,
        XEMACPS_RECV
    );
    if (Status != XST_SUCCESS) {
        xil_printf("broke: bdring (rx) clone\n\r");
        return XST_FAILURE;
    }
    
    Status = XEmacPs_BdRingClone(
        &(XEmacPs_GetTxRing(&emac)),
        &bdtemplate,
        XEMACPS_SEND
    );
    if (Status != XST_SUCCESS) {
        xil_printf("broke: bdring (tx) clone\n\r");
        return XST_FAILURE;
    }

    XEmacPs_SetQueuePtr(
        &emac,
        emac.RxBdRing.BaseBdAddr,
        0,
        XEMACPS_RECV
    );

    Xil_SetTlbAttributes(emac.RxBdRing.BaseBdAddr, STRONG_ORDERED);

    XEmacPs_Start(&emac);
    
    return XST_SUCCESS;

}
int main()
{
    init_platform();

    xil_printf("Hello World\n\r");
    init();
    xil_printf("init success!\n\r");
    
    cleanup_platform();
    return 0;
}
