// The DSLogic driver drives the original DreamSourceLab DSLogic (USB 2A0E:0001)
// as a logic analyser.
//
// The protocol follows DSView libsigrok4DSL. The FX2 firmware and FPGA
// bitstreams in dslogic_firmware come from DSView; see
// THIRD_PARTY_NOTICES.

package logic

import _ "embed"

var (
	//go:embed dslogic_firmware/DSLogic.fw
	fx2Firmware []byte
	//go:embed dslogic_firmware/DSLogic33.bin
	fpga33 []byte
	//go:embed dslogic_firmware/DSLogic50.bin
	fpga50 []byte
)
