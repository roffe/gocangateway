# goCAN Gateway

A gRPC <-> CAN gateway written in Go for use in [txlogger](https://github.com/roffe/txlogger).

## Why does this exist?

I need to be able to use 32-bit dll J2534 drivers from my 64-bit application so this gateway acts as a bridge between my application and the CAN hardware.