package fec

// GF(256) arithmetic with AES polynomial 0x11b.

var expTable [512]byte
var logTable [256]byte

func init() {
	// Build exp and log tables using a primitive generator g=0x03 under AES polynomial 0x11b.
	// This ensures all 255 non-zero elements are covered.
	exp := byte(1)
	for i := 0; i < 255; i++ {
		expTable[i] = exp
		logTable[exp] = byte(i)
		// multiply by 3: (x*2) ^ x under GF(2)
		exp = xtime(exp) ^ exp
	}
	for i := 255; i < 512; i++ {
		expTable[i] = expTable[i-255]
	}
}

func xtime(x byte) byte {
	if x&0x80 != 0 {
		return (x << 1) ^ 0x1b
	}
	return x << 1
}

func gf256Mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	la := logTable[a]
	lb := logTable[b]
	return expTable[int(la)+int(lb)]
}

func gf256Inv(a byte) byte {
	if a == 0 {
		return 0
	}
	return expTable[255-int(logTable[a])]
}

// Test helpers (exported) to access tables in tests without reimplementing
func GF256Exp(i int) byte  { return expTable[i%255] }
func GF256Log(a byte) byte { return logTable[a] }
