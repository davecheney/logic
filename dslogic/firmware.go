package dslogic

import _ "embed"

var (
	//go:embed firmware/DSLogic.fw
	dslogicFirmware []byte
	//go:embed firmware/DSLogic33.bin
	fpga33 []byte
	//go:embed firmware/DSLogic50.bin
	fpga50 []byte
)
