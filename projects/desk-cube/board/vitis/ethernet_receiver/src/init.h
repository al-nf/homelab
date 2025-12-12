#ifndef INIT_H
#define INIT_H

#include "xemacps.h"
#include "xscugic.h"

extern XEmacPs Emac;
extern XScuGic Intc;

void init_emac();
void init_intc();
void RxIntrHandler(void *Callback);

#endif // INIT_H