package main

import (
	"log"
	"net"

	"github.com/mdlayher/ethernet"
	"github.com/mdlayher/packet"
)

func sendFrame(payload []byte) error {
	// SET LATER
	socMAC := net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	piMAC := net.HardwareAddr{0xb8, 0x27, 0xeb, 0x52, 0x77, 0xfb}

	iface, err := net.InterfaceByName("eth0")
	if err != nil {
		log.Printf("failed to get interface: %v", err)
		return err
	}

	// build frame
	f := &ethernet.Frame{
		Destination: socMAC,
		Source:      piMAC,
		EtherType:   0xcccc,
		Payload:     payload,
	}

	frameBytes, err := f.MarshalBinary()
	if err != nil {
		log.Printf("failed to marshal frame: %v", err)
		return err
	}

	// open connection
	conn, err := packet.Listen(iface, packet.Raw, 0xcccc, nil)
	if err != nil {
		log.Printf("failed to open packet connection: %v", err)
		return err
	}
	defer conn.Close()

	// send frame
	addr := &packet.Addr{
		HardwareAddr: socMAC,
	}
	if _, err := conn.WriteTo(frameBytes, addr); err != nil {
		log.Printf("failed to send frame: %v", err)
		return err
	}

	log.Printf("sent %d bytes to %s", len(frameBytes), socMAC.String())
	return nil
}
