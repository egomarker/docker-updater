package util

import (
	"crypto/rand"
	"time"
)

var crockfordBase32 = [32]byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'J', 'K', 'M', 'N', 'P', 'Q', 'R', 'S', 'T', 'V', 'W', 'X', 'Y', 'Z'}

func NewULID() (string, error) {
	var data [16]byte
	ms := uint64(time.Now().UTC().UnixMilli())
	data[0] = byte(ms >> 40)
	data[1] = byte(ms >> 32)
	data[2] = byte(ms >> 24)
	data[3] = byte(ms >> 16)
	data[4] = byte(ms >> 8)
	data[5] = byte(ms)
	if _, err := rand.Read(data[6:]); err != nil {
		return "", err
	}

	var out [26]byte
	out[0] = crockfordBase32[(data[0]&224)>>5]
	out[1] = crockfordBase32[data[0]&31]
	out[2] = crockfordBase32[(data[1]&248)>>3]
	out[3] = crockfordBase32[((data[1]&7)<<2)|((data[2]&192)>>6)]
	out[4] = crockfordBase32[(data[2]&62)>>1]
	out[5] = crockfordBase32[((data[2]&1)<<4)|((data[3]&240)>>4)]
	out[6] = crockfordBase32[((data[3]&15)<<1)|((data[4]&128)>>7)]
	out[7] = crockfordBase32[(data[4]&124)>>2]
	out[8] = crockfordBase32[((data[4]&3)<<3)|((data[5]&224)>>5)]
	out[9] = crockfordBase32[data[5]&31]
	out[10] = crockfordBase32[(data[6]&248)>>3]
	out[11] = crockfordBase32[((data[6]&7)<<2)|((data[7]&192)>>6)]
	out[12] = crockfordBase32[(data[7]&62)>>1]
	out[13] = crockfordBase32[((data[7]&1)<<4)|((data[8]&240)>>4)]
	out[14] = crockfordBase32[((data[8]&15)<<1)|((data[9]&128)>>7)]
	out[15] = crockfordBase32[(data[9]&124)>>2]
	out[16] = crockfordBase32[((data[9]&3)<<3)|((data[10]&224)>>5)]
	out[17] = crockfordBase32[data[10]&31]
	out[18] = crockfordBase32[(data[11]&248)>>3]
	out[19] = crockfordBase32[((data[11]&7)<<2)|((data[12]&192)>>6)]
	out[20] = crockfordBase32[(data[12]&62)>>1]
	out[21] = crockfordBase32[((data[12]&1)<<4)|((data[13]&240)>>4)]
	out[22] = crockfordBase32[((data[13]&15)<<1)|((data[14]&128)>>7)]
	out[23] = crockfordBase32[(data[14]&124)>>2]
	out[24] = crockfordBase32[((data[14]&3)<<3)|((data[15]&224)>>5)]
	out[25] = crockfordBase32[data[15]&31]

	return string(out[:]), nil
}
