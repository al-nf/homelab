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
#include <xil_exception.h>
#include <xil_types.h>
#include <xparameters.h>
#include <xil_printf.h>
#include <xscugic.h>
#include <xemacps.h>
#include <xil_mmu.h>
#include <xil_cache.h>
#include "platform.h"

XScuGic intc;
XEmacPs emac;

/*
void myassertcb(const char8 *File, s32 Line) {
    xil_printf("assert in %s, line %d\r\n", File, Line);
}
*/

void rx_handler(void* cb) {
    XEmacPs* e = cb;

    XEmacPs_Bd* bd;
    u32 n = XEmacPs_BdRingFromHwRx(
        &(XEmacPs_GetRxRing(e)),
        1,
        &bd
    );
    if (n == 0)
        return;

    u8* buf = XEmacPs_BdGetAddressRx(bd);
    u32 len = XEmacPs_BdGetLength(bd);
    Xil_DCacheInvalidateRange(buf, len);

    if (len > 14 && buf[14] == 0xAB) {
        xil_printf("hi!\r\n");
    }

    XEmacPs_BdRingFree(
        &(XEmacPs_GetRxRing(e)),
        1,
        bd
    );
    XEmacPs_BdRingAlloc(
        &(XEmacPs_GetRxRing(e)),
        1,
        &bd
    );
    XEmacPs_BdSetAddressRx(
        bd,
        buf
    );
    XEmacPs_BdRingToHw(
        &(XEmacPs_GetRxRing(e)),
        1,
        bd
    );
}

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

    XEmacPs_SetHandler(&emac, XEMACPS_HANDLER_DMARECV, rx_handler, &emac);
    
    // register GEM interrupt
    XScuGic_Connect(
        &intc,
        XPS_GEM0_INT_ID,
        (Xil_InterruptHandler)XEmacPs_IntrHandler,
        &emac
    );
    XScuGic_Enable(&intc, XPS_GEM0_INT_ID);


    // BDRINGS
    Xil_SetTlbAttributes(0xff00000, STRONG_ORDERED);

    Status = XEmacPs_BdRingCreate(
        &(XEmacPs_GetRxRing(&emac)),
        0xff00000,
        0xff00000,
        XEMACPS_BD_ALIGNMENT,
        XEMACPS_MAX_RXBD
    );
    if (Status != XST_SUCCESS) {
        xil_printf("broke: bdring (rx) create\n\r");
        return XST_FAILURE;
    }
    
    Status = XEmacPs_BdRingCreate(
        &(XEmacPs_GetTxRing(&emac)),
        0xff10000,
        0xff10000,
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


    XEmacPs_Start(&emac);
    
    return XST_SUCCESS;

}

int main()
{
    init_platform();

    xil_printf("Hello World\n\r");
    init();
    xil_printf("init success!\n\r");

    while (1) {
        __wfi();
    }
    
    cleanup_platform();
    return 0;
}
