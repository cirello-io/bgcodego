package bgcodego

import "slices"

const (
	meatpackCommandEnablePacking   byte = 251
	meatpackCommandDisablePacking  byte = 250
	meatpackCommandResetAll        byte = 249
	meatpackCommandEnableNoSpaces  byte = 247
	meatpackCommandDisableNoSpaces byte = 246
	meatpackCommandSignalByte      byte = 0xFF

	meatpackBothUnpackable   byte = 0b11111111
	meatpackSecondNotPacked  byte = 0b11110000
	meatpackFirstNotPacked   byte = 0b00001111
	meatpackNextPackedFirst  byte = 0b00000001
	meatpackNextPackedSecond byte = 0b00000010
)

type mpUnbinarize struct {
	unbinarizing   bool
	nospaceEnabled bool
	cmdActive      bool
	charBuf        byte
	cmdCount       int
	fullCharQueue  int
	charOutBuf     []byte //:= make([]byte, 2)
	charOutCount   int
	addSpace       bool
}

func (mpu *mpUnbinarize) handleCommand(c byte) {
	switch c {
	case meatpackCommandEnablePacking:
		mpu.unbinarizing = true
	case meatpackCommandDisablePacking:
		mpu.unbinarizing = false
	case meatpackCommandEnableNoSpaces:
		mpu.nospaceEnabled = true
	case meatpackCommandDisableNoSpaces:
		mpu.nospaceEnabled = false
	case meatpackCommandResetAll:
		mpu.unbinarizing = false
	}
}
func (mpu *mpUnbinarize) handleOutputChar(c byte) {
	mpu.charOutBuf[mpu.charOutCount] = c
	mpu.charOutCount++
}

func (mpu *mpUnbinarize) getChar(c byte) byte {
	switch c {
	case 0b0000:
		return '0'
	case 0b0001:
		return '1'
	case 0b0010:
		return '2'
	case 0b0011:
		return '3'
	case 0b0100:
		return '4'
	case 0b0101:
		return '5'
	case 0b0110:
		return '6'
	case 0b0111:
		return '7'
	case 0b1000:
		return '8'
	case 0b1001:
		return '9'
	case 0b1010:
		return '.'
	case 0b1011:
		if mpu.nospaceEnabled {
			return 'E'
		}
		return ' '
	case 0b1100:
		return '\n'
	case 0b1101:
		return 'G'
	case 0b1110:
		return 'X'
	}
	return 0
}

func (mpu *mpUnbinarize) unpackChars(pk byte) (byte, []byte) {
	out := byte(0)
	charsOut := make([]byte, 2)
	if (pk & meatpackFirstNotPacked) == meatpackFirstNotPacked {
		out |= meatpackNextPackedFirst
	} else {
		charsOut[0] = mpu.getChar(pk & 0xF)
	}
	if (pk & meatpackSecondNotPacked) == meatpackSecondNotPacked {
		out |= meatpackNextPackedSecond
	} else {
		charsOut[1] = mpu.getChar((pk >> 4) & 0xF)
	}
	return out, charsOut
}

func (mpu *mpUnbinarize) handleRxChar(c byte) {
	if !mpu.unbinarizing {
		mpu.handleOutputChar(c)
		return
	}

	if mpu.fullCharQueue > 0 {
		mpu.handleOutputChar(c)
		if mpu.charBuf > 0 {
			mpu.handleOutputChar(mpu.charBuf)
			mpu.charBuf = 0
		}
		mpu.fullCharQueue--
		return
	}
	res, buf := mpu.unpackChars(c)
	if (res & meatpackNextPackedFirst) != 0 {
		mpu.fullCharQueue++
		if (res & meatpackNextPackedSecond) != 0 {
			mpu.fullCharQueue++
		} else {
			mpu.charBuf = buf[1]
		}
		return
	}

	mpu.handleOutputChar(buf[0])
	if buf[0] == '\n' {
		return
	}
	if (res & meatpackNextPackedSecond) != 0 {
		mpu.fullCharQueue++
		return
	}
	mpu.handleOutputChar(buf[1])
}

func (mpu *mpUnbinarize) getResultChar(charsOut []byte) int {
	if mpu.charOutCount > 0 {
		copy(charsOut, mpu.charOutBuf[:mpu.charOutCount])
		res := mpu.charOutCount
		mpu.charOutCount = 0
		return res
	}
	return 0
}

func unbinarize(src []byte) string {
	mpu := &mpUnbinarize{
		charOutBuf: make([]byte, 2),
	}
	unbinBuffer := make([]byte, 0)
	for _, c := range src {
		switch {
		case c == meatpackCommandSignalByte && mpu.cmdCount > 0:
			mpu.cmdActive = true
			mpu.cmdCount = 0
		case c == meatpackCommandSignalByte:
			mpu.cmdCount++
		case mpu.cmdActive:
			mpu.handleCommand(c)
			mpu.cmdActive = false
		default:
			if mpu.cmdCount > 0 {
				mpu.handleRxChar(meatpackCommandSignalByte)
				mpu.cmdCount = 0
			}
			mpu.handleRxChar(c)
		}

		unbinChar := make([]byte, 2)
		charCount := mpu.getResultChar(unbinChar)
		for i := 0; i < charCount; i++ {
			unbinBufLen := len(unbinBuffer)
			if unbinChar[i] == 'G' && (unbinBufLen == 0 || unbinBuffer[unbinBufLen-1] == '\n') {
				mpu.addSpace = true
			} else if unbinChar[i] == '\n' {
				mpu.addSpace = false
			}
			if mpu.addSpace && (unbinBufLen == 0 || unbinBuffer[unbinBufLen-1] != ' ') && isGlineParameter(unbinChar[i]) {
				unbinBuffer = append(unbinBuffer, ' ')
			}
			if unbinChar[i] != '\n' || unbinBufLen == 0 || unbinBuffer[unbinBufLen-1] != '\n' {
				unbinBuffer = append(unbinBuffer, unbinChar[i])
			}
		}
	}

	return string(unbinBuffer)
}

func isGlineParameter(c byte) bool {
	parameters := []byte{'X', 'Y', 'Z', 'E', 'F', 'I', 'J', 'R', 'P', 'W', 'H', 'C', 'A'}
	return slices.Contains(parameters, c)
}
