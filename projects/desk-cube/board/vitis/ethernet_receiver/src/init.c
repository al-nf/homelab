#include "init.h"
#include "xparameters.h"
#include "xil_exception.h"

#define RX_BD_CNT 16
#define RX_BUF_SIZE 1536

XEmacPs Emac;
XEmacPs_Bd RxBdSpace[RX_BD_CNT] __attribute__((aligned(64)));
u8 RxBuffers[RX_BD_CNT][RX_BUF_SIZE] __attribute__((aligned(64)));

XScuGic Intc;

void RxIntrHandler(void *Callback) {
    XEmacPs *EmacPtr = (XEmacPs *)Callback;
    // do stuff
}

void init_emac() {
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
    XScuGic_Config* CfgPtr = XScuGic_LookupConfig(0);
    XScuGic_CfgInitialize(&Intc, CfgPtr, CfgPtr->CpuBaseAddress); 
    
    XScuGic_Connect(&Intc, XPAR_XEMACPS_0_INTR, (Xil_ExceptionHandler)XEmacPs_IntrHandler, &Emac);
    
    XScuGic_Enable(&Intc, XPAR_XEMACPS_0_INTR);
    Xil_ExceptionRegisterHandler(XIL_EXCEPTION_ID_INT, 
        (Xil_ExceptionHandler)XScuGic_InterruptHandler, &Intc);
    Xil_ExceptionEnable();
}