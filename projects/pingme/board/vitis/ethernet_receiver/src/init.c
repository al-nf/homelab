#include "init.h"
#include "xparameters.h"
#include "xil_exception.h"
#include "xil_printf.h"
#include "xil_cache.h"
#include <string.h>

#define RX_BD_CNT 16
#define RX_BUF_SIZE 1536

XEmacPs Emac;
XEmacPs_Bd RxBdSpace[RX_BD_CNT] __attribute__((aligned(XEMACPS_BD_ALIGNMENT))) __attribute__((section(".data")));
u8 RxBuffers[RX_BD_CNT][RX_BUF_SIZE] __attribute__((aligned(64))) __attribute__((section(".data")));

XScuGic Intc;

void RxHandler(void *Callback) {
    XEmacPs *EmacPtr = (XEmacPs *)Callback;
    XEmacPs_BdRing *RxRingPtr = &(XEmacPs_GetRxRing(EmacPtr));
    XEmacPs_Bd *BdPtr;
    int BdCount;
    
    BdCount = XEmacPs_BdRingFromHwRx(RxRingPtr, RX_BD_CNT, &BdPtr);
    if (BdCount <= 0) {
        return;
    }
    
    XEmacPs_Bd *CurBdPtr = BdPtr;
    for (int i = 0; i < BdCount; i++) {
        Xil_DCacheInvalidateRange((UINTPTR)CurBdPtr, sizeof(XEmacPs_Bd));
        
        u8 *bdBytes = (u8 *)CurBdPtr;
        u32 bdAddr = bdBytes[0] | (bdBytes[1] << 8) | (bdBytes[2] << 16) | (bdBytes[3] << 24);
        u32 bdCtrl = bdBytes[4] | (bdBytes[5] << 8) | (bdBytes[6] << 16) | (bdBytes[7] << 24);
        u16 FrameLength = (u16)(bdCtrl & 0x1FFF);
        u8 *FramePtr = (u8 *)(bdAddr & ~0x3);
        
        xil_printf("\r\nFrame %d: %d bytes (BD: addr=0x%08x ctrl=0x%08x)\r\n", 
            i+1, FrameLength, bdAddr, bdCtrl);
        
        if (FrameLength > RX_BUF_SIZE || FrameLength == 0) {
            xil_printf("  Invalid length!\r\n");
            CurBdPtr = XEmacPs_BdRingNext(RxRingPtr, CurBdPtr);
            continue;
        }
        
        Xil_DCacheInvalidateRange((UINTPTR)FramePtr, FrameLength);
        xil_printf("dst: %02x:%02x:%02x:%02x:%02x:%02x\r\n", 
            FramePtr[0], FramePtr[1], FramePtr[2], FramePtr[3], FramePtr[4], FramePtr[5]);
        xil_printf("src: %02x:%02x:%02x:%02x:%02x:%02x\r\n", 
            FramePtr[6], FramePtr[7], FramePtr[8], FramePtr[9], FramePtr[10], FramePtr[11]);
        xil_printf("EtherType: 0x%02x%02x\r\n", FramePtr[12], FramePtr[13]);
        
        u32 printLen = (FrameLength > 64) ? 64 : FrameLength;
        xil_printf("Data:\r\n");
        for (u32 j = 0; j < printLen; j++) {
            if (j % 16 == 0) xil_printf("    ");
            xil_printf("%02x ", FramePtr[j]);
            if ((j + 1) % 16 == 0) xil_printf("\r\n");
        }
        if (printLen % 16 != 0) xil_printf("\r\n");
        
        CurBdPtr = XEmacPs_BdRingNext(RxRingPtr, CurBdPtr);
    }
    
    CurBdPtr = BdPtr;
    for (int i = 0; i < BdCount; i++) {
        int bdIndex = ((u8*)CurBdPtr - (u8*)RxBdSpace) / sizeof(XEmacPs_Bd);
        u32 bufAddr = (UINTPTR)RxBuffers[bdIndex];
        
        if (bdIndex == (RX_BD_CNT - 1)) {
            XEmacPs_BdWrite(CurBdPtr, XEMACPS_BD_ADDR_OFFSET, bufAddr | XEMACPS_RXBUF_WRAP_MASK);
        } else {
            XEmacPs_BdWrite(CurBdPtr, XEMACPS_BD_ADDR_OFFSET, bufAddr);
        }
        
        XEmacPs_BdWrite(CurBdPtr, XEMACPS_BD_STAT_OFFSET, 0);
        
        CurBdPtr = XEmacPs_BdRingNext(RxRingPtr, CurBdPtr);
    }
    
    XEmacPs_Bd *LastBd = &RxBdSpace[RX_BD_CNT - 1];
    u32 lastAddr = XEmacPs_BdRead(LastBd, XEMACPS_BD_ADDR_OFFSET);
    if (!(lastAddr & XEMACPS_RXBUF_WRAP_MASK)) {
        XEmacPs_BdWrite(LastBd, XEMACPS_BD_ADDR_OFFSET, lastAddr | XEMACPS_RXBUF_WRAP_MASK);
    }
    
    Xil_DCacheFlushRange((UINTPTR)RxBdSpace, sizeof(RxBdSpace));
    
    int Status = XEmacPs_BdRingFree(RxRingPtr, BdCount, BdPtr);
    if (Status != XST_SUCCESS) {
        xil_printf("ERROR: bdring free failed\r\n");
        return;
    }
    
    XEmacPs_Bd *NewBdPtr;
    Status = XEmacPs_BdRingAlloc(RxRingPtr, BdCount, &NewBdPtr);
    if (Status != XST_SUCCESS) {
        xil_printf("ERROR: bdring alloc failed\r\n");
        return;
    }
    
    Status = XEmacPs_BdRingToHw(RxRingPtr, BdCount, NewBdPtr);
    if (Status != XST_SUCCESS) {
        xil_printf("ERROR: bdringtohw failed\r\n");
        return;
    }

    u32 bdRingAddr = (u32)((UINTPTR)RxBdSpace);
    XEmacPs_WriteReg(EmacPtr->Config.BaseAddress, XEMACPS_RXQBASE_OFFSET, bdRingAddr);
    
    u32 rxStatus = XEmacPs_ReadReg(EmacPtr->Config.BaseAddress, XEMACPS_RXSR_OFFSET);
    if (rxStatus != 0) {
        XEmacPs_WriteReg(EmacPtr->Config.BaseAddress, XEMACPS_RXSR_OFFSET, rxStatus);
    }
    
    u32 hwCnt = XEmacPs_BdRingGetCnt(RxRingPtr);
    u32 freeCnt = XEmacPs_BdRingGetFreeCnt(RxRingPtr);
    
    static int dumpCount = 0;
    if (dumpCount < 2) {
        dumpCount++;
        int bdsToDump[] = {0, 1, 2, 3, 14, 15};
        for (int j = 0; j < 6; j++) {
            int i = bdsToDump[j];
            XEmacPs_Bd *DbgBd = &RxBdSpace[i];
            u32 addr = XEmacPs_BdRead(DbgBd, XEMACPS_BD_ADDR_OFFSET);
            u32 stat = XEmacPs_BdRead(DbgBd, XEMACPS_BD_STAT_OFFSET);
        }
        
        u32 rxStatus = XEmacPs_ReadReg(EmacPtr->Config.BaseAddress, XEMACPS_RXSR_OFFSET);
        u32 isr = XEmacPs_ReadReg(EmacPtr->Config.BaseAddress, XEMACPS_ISR_OFFSET);
    }
}

void RxIntrHandler(void *Callback) {
    XEmacPs_IntrHandler(Callback);
}

void init_emac() {
    XEmacPs_Config* CfgPtr = XEmacPs_LookupConfig(0);
    XEmacPs_CfgInitialize(&Emac, CfgPtr, CfgPtr->BaseAddress);
    XEmacPs_BdRing *RxRingPtr = &(XEmacPs_GetRxRing(&Emac));
    u32 BdSpaceAddr = (u32)((UINTPTR)RxBdSpace);
    int Status = XEmacPs_BdRingCreate(RxRingPtr, BdSpaceAddr, BdSpaceAddr, XEMACPS_BD_ALIGNMENT, RX_BD_CNT);
    XEmacPs_Bd *BdPtr;
    XEmacPs_Bd *BdStart;
    XEmacPs_BdRingAlloc(RxRingPtr, RX_BD_CNT, &BdPtr);
    BdStart = BdPtr;
    
    for (int i = 0; i < RX_BD_CNT; i++) {
        XEmacPs_BdClear(BdPtr);
        
        XEmacPs_BdSetAddressRx(BdPtr, (UINTPTR)RxBuffers[i]);
        
        if (i == (RX_BD_CNT - 1)) {
            XEmacPs_BdSetLast(BdPtr);
        }
        
        BdPtr = XEmacPs_BdRingNext(RxRingPtr, BdPtr);
    }
    
    Xil_DCacheFlushRange((UINTPTR)RxBdSpace, sizeof(RxBdSpace));
    
    XEmacPs_BdRingToHw(RxRingPtr, RX_BD_CNT, BdStart);
    XEmacPs_WriteReg(Emac.Config.BaseAddress, XEMACPS_RXQBASE_OFFSET, BdSpaceAddr);
    XEmacPs_SetOptions(&Emac, XEMACPS_PROMISC_OPTION);
    XEmacPs_SetOptions(&Emac, XEMACPS_BROADCAST_OPTION);
    XEmacPs_SetOperatingSpeed(&Emac, 100);
    u32 Reg = XEmacPs_ReadReg(Emac.Config.BaseAddress, XEMACPS_NWCTRL_OFFSET);
    Reg |= XEMACPS_NWCTRL_TXEN_MASK | XEMACPS_NWCTRL_RXEN_MASK;
    XEmacPs_WriteReg(Emac.Config.BaseAddress, XEMACPS_NWCTRL_OFFSET, Reg);
    XEmacPs_SetHandler(&Emac, XEMACPS_HANDLER_DMARECV, (void *)RxHandler, &Emac);
    XEmacPs_IntEnable(&Emac, XEMACPS_IXR_FRAMERX_MASK | XEMACPS_IXR_RX_ERR_MASK);
    u32 IntMask = XEmacPs_ReadReg(Emac.Config.BaseAddress, XEMACPS_IMR_OFFSET);
    print("EMAC started\r\n");
}


void init_intc() {
    XScuGic_Config* CfgPtr = XScuGic_LookupConfig(XPAR_SCUGIC_SINGLE_DEVICE_ID);
    XScuGic_CfgInitialize(&Intc, CfgPtr, CfgPtr->CpuBaseAddress);
    XScuGic_Connect(&Intc, XPAR_XEMACPS_0_INTR, (Xil_ExceptionHandler)XEmacPs_IntrHandler, &Emac);
    XScuGic_Enable(&Intc, XPAR_XEMACPS_0_INTR);
    Xil_ExceptionRegisterHandler(XIL_EXCEPTION_ID_INT, (Xil_ExceptionHandler)XScuGic_InterruptHandler, &Intc);
    Xil_ExceptionEnable();
}