#include "init.h"
#include "xparameters.h"
#include "xil_exception.h"
#include "xil_printf.h"
#include "xil_cache.h"

#define RX_BD_CNT 16
#define RX_BUF_SIZE 1536

XEmacPs Emac;
XEmacPs_Bd RxBdSpace[RX_BD_CNT] __attribute__((aligned(64)));
u8 RxBuffers[RX_BD_CNT][RX_BUF_SIZE] __attribute__((aligned(64)));

XScuGic Intc;

void RxIntrHandler(void *Callback) {
    XEmacPs *EmacPtr = (XEmacPs *)Callback;
    XEmacPs_BdRing *RxRingPtr = &(XEmacPs_GetRxRing(EmacPtr));
    XEmacPs_Bd *BdPtr;
    u32 BdCount;
    u32 FrameLength;
    u8 *FramePtr;
    int Status;
    
    BdCount = XEmacPs_BdRingFromHwRx(RxRingPtr, XEMACPS_RECV_BD_CNT_MAX, &BdPtr);
    
    // process each frame
    for (u32 i = 0; i < BdCount; i++) {
        FramePtr = (u8 *)XEmacPs_BdGetAddressRx(BdPtr);
        FrameLength = XEmacPs_BdGetLength(BdPtr);
        
        Xil_DCacheInvalidateRange((UINTPTR)FramePtr, FrameLength);
        
        xil_printf("\r\nFrame received (%lu bytes)\r\n", FrameLength);
        xil_printf("  Dst MAC: %02x:%02x:%02x:%02x:%02x:%02x\r\n", FramePtr[0], FramePtr[1], 
            FramePtr[2], FramePtr[3], 
            FramePtr[4], FramePtr[5]);
        xil_printf("  Src MAC: %02x:%02x:%02x:%02x:%02x:%02x\r\n", FramePtr[6], FramePtr[7], 
            FramePtr[8], FramePtr[9], 
            FramePtr[10], FramePtr[11]);
        xil_printf("  EtherType: 0x%02x%02x\r\n", FramePtr[12], FramePtr[13]);
        
        // do things
        
        // next
        BdPtr = XEmacPs_BdRingNext(RxRingPtr, BdPtr);
    }
    
    // reset
    if (BdCount > 0) {
        Status = XEmacPs_BdRingFree(RxRingPtr, BdCount, 
            XEmacPs_BdRingFromHwRx(RxRingPtr, BdCount, &BdPtr));

        if (Status != XST_SUCCESS) {
            print("Failed to free RX BDs\r\n");
            return;
        }
        
        Status = XEmacPs_BdRingAlloc(RxRingPtr, BdCount, &BdPtr);
        if (Status != XST_SUCCESS) {
            print("Failed to reallocate RX BDs\r\n");
            return;
        }
        
        XEmacPs_Bd *CurBdPtr = BdPtr;
        for (u32 i = 0; i < BdCount; i++) {
            u8 *BufAddr = (u8 *)XEmacPs_BdGetAddressRx(CurBdPtr);
            XEmacPs_BdSetAddressRx(CurBdPtr, (UINTPTR)BufAddr);
            CurBdPtr = XEmacPs_BdRingNext(RxRingPtr, CurBdPtr);
        }
        
        Status = XEmacPs_BdRingToHw(RxRingPtr, BdCount, BdPtr);
        if (Status != XST_SUCCESS) {
            print("Failed to give BDs to HW\r\n");
        }
    }
}

void init_emac() {
    int Status;
    XEmacPs_Config* CfgPtr = XEmacPs_LookupConfig(0);
    XEmacPs_CfgInitialize(&Emac, CfgPtr, CfgPtr->BaseAddress);
    
    XEmacPs_BdRing *RxRingPtr = &(XEmacPs_GetRxRing(&Emac));
    
    XEmacPs_BdRingCreate(RxRingPtr, (UINTPTR)RxBdSpace, (UINTPTR)RxBdSpace,
        XEMACPS_BD_ALIGNMENT, RX_BD_CNT);
    
    XEmacPs_Bd *BdPtr;
    XEmacPs_BdRingAlloc(RxRingPtr, RX_BD_CNT, &BdPtr);
    for (int i = 0; i < RX_BD_CNT; i++) {
        XEmacPs_BdSetAddressRx(BdPtr, (UINTPTR)RxBuffers[i]);
        BdPtr = XEmacPs_BdRingNext(RxRingPtr, BdPtr);
    }
    XEmacPs_BdRingToHw(RxRingPtr, RX_BD_CNT, RxRingPtr->FirstBdAddr);

    XEmacPs_SetOptions(&Emac, XEMACPS_PROMISC_OPTION);
    XEmacPs_SetOptions(&Emac, XEMACPS_RECEIVER_ENABLE_OPTION);
    XEmacPs_SetOptions(&Emac, XEMACPS_BROADCAST_OPTION);
    XEmacPs_SetOptions(&Emac, XEMACPS_GIGABIT_MODE_ENABLE_OPTION);

    u8 MacAddr[6] = {0x00, 0x18, 0x3E, 0x04, 0xEC, 0xF9};
    XEmacPs_SetMacAddress(&Emac, MacAddr, 1);

    XEmacPs_SetOperatingSpeed(&Emac, 1000);
    
    XEmacPs_SetHandler(&Emac, XEMACPS_HANDLER_DMARX, (void *)RxIntrHandler, &Emac);
    XEmacPs_IntEnable(&Emac, XEMACPS_IXR_FRAMERX_MASK);
    
    XEmacPs_Start(&Emac);
}


void init_intc() {
    XScuGic_Config* CfgPtr = XScuGic_LookupConfig(XPAR_SCUGIC_SINGLE_DEVICE_ID);
    XScuGic_CfgInitialize(&Intc, CfgPtr, CfgPtr->CpuBaseAddress); 
    
    XScuGic_Connect(&Intc, XPAR_XEMACPS_0_INTR, (Xil_ExceptionHandler)XEmacPs_IntrHandler, &Emac);
    
    XScuGic_Enable(&Intc, XPAR_XEMACPS_0_INTR);
    Xil_ExceptionRegisterHandler(XIL_EXCEPTION_ID_INT, 
        (Xil_ExceptionHandler)XScuGic_InterruptHandler, &Intc);
    Xil_ExceptionEnable();
}