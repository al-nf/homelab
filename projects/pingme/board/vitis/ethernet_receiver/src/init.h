#ifndef INIT_H
#define INIT_H

#include "xemacps.h"
#include "xemacps_bdring.h"
#include "xscugic.h"

#define RX_BD_CNT 16

extern XEmacPs Emac;
extern XScuGic Intc;
extern XEmacPs_Bd RxBdSpace[RX_BD_CNT];

void init_emac();
void init_intc();
void RxIntrHandler(void *Callback);

#endif // INIT_H