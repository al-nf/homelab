package main

import "syscall"

const terminalGet = syscall.TIOCGETA
const terminalSet = syscall.TIOCSETA
