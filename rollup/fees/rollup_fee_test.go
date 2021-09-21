package fees

import (
	"errors"
	"math/big"
	"testing"

	"github.com/MetisProtocol/l2geth/params"
	"github.com/MetisProtocol/l2geth/common"
)

var l1GasLimitTests = map[string]struct {
	data     []byte
	overhead uint64
	expect   *big.Int
}{
	"simple":          {[]byte{}, 0, big.NewInt(0)},
	"simple-overhead": {[]byte{}, 10, big.NewInt(10)},
	"zeros":           {[]byte{0x00, 0x00, 0x00, 0x00}, 10, big.NewInt(26)},
	"ones":            {[]byte{0x01, 0x02, 0x03, 0x04}, 200, big.NewInt(16*4 + 200)},
}

func TestL1GasLimit(t *testing.T) {
	for name, tt := range l1GasLimitTests {
		t.Run(name, func(t *testing.T) {
			got := calculateL1GasLimit(tt.data, tt.overhead)
			if got.Cmp(tt.expect) != 0 {
				t.Fatal("Calculated gas limit does not match")
			}
		})
	}
}

var feeTests = map[string]struct {
	dataLen    int
	l1GasPrice uint64
	l2GasLimit uint64
	l2GasPrice uint64
}{
	"simple": {
		dataLen:    10,
		l1GasPrice: params.GWei,
		l2GasLimit: 437118,
		l2GasPrice: params.GWei,
	},
	"zero-l2-gasprice": {
		dataLen:    10,
		l1GasPrice: params.GWei,
		l2GasLimit: 196205,
		l2GasPrice: 0,
	},
	"one-l2-gasprice": {
		dataLen:    10,
		l1GasPrice: params.GWei,
		l2GasLimit: 196205,
		l2GasPrice: 1,
	},
	"zero-l1-gasprice": {
		dataLen:    10,
		l1GasPrice: 0,
		l2GasLimit: 196205,
		l2GasPrice: params.GWei,
	},
	"one-l1-gasprice": {
		dataLen:    10,
		l1GasPrice: 1,
		l2GasLimit: 23255,
		l2GasPrice: params.GWei,
	},
	"zero-gasprices": {
		dataLen:    10,
		l1GasPrice: 0,
		l2GasLimit: 23255,
		l2GasPrice: 0,
	},
	"max-gaslimit": {
		dataLen:    10,
		l1GasPrice: params.GWei,
		l2GasLimit: 99_970_000,
		l2GasPrice: params.GWei,
	},
	"larger-divisor": {
		dataLen:    10,
		l1GasPrice: 0,
		l2GasLimit: 10,
		l2GasPrice: 0,
	},
}

func TestCalculateRollupFee(t *testing.T) {
	for name, tt := range feeTests {
		t.Run(name, func(t *testing.T) {
			data := make([]byte, tt.dataLen)
			l1GasPrice := new(big.Int).SetUint64(tt.l1GasPrice)
			l2GasLimit := new(big.Int).SetUint64(tt.l2GasLimit)
			l2GasPrice := new(big.Int).SetUint64(tt.l2GasPrice)

			fee := EncodeTxGasLimit(data, l1GasPrice, l2GasLimit, l2GasPrice)
			decodedGasLimit := DecodeL2GasLimit(fee)
			roundedL2GasLimit := Ceilmod(l2GasLimit, BigTenThousand)
			if roundedL2GasLimit.Cmp(decodedGasLimit) != 0 {
				t.Errorf("rollup fee check failed: expected %d, got %d", l2GasLimit.Uint64(), decodedGasLimit)
			}
		})
	}
}

func TestPaysEnough(t *testing.T) {
	tests := map[string]struct {
		opts *PaysEnoughOpts
		err  error
	}{
		"missing-gas-price": {
			opts: &PaysEnoughOpts{
				UserFee:       nil,
				ExpectedFee:   new(big.Int),
				ThresholdUp:   nil,
				ThresholdDown: nil,
			},
			err: errMissingInput,
		},
		"missing-fee": {
			opts: &PaysEnoughOpts{
				UserFee:       nil,
				ExpectedFee:   nil,
				ThresholdUp:   nil,
				ThresholdDown: nil,
			},
			err: errMissingInput,
		},
		"equal-fee": {
			opts: &PaysEnoughOpts{
				UserFee:       common.Big1,
				ExpectedFee:   common.Big1,
				ThresholdUp:   nil,
				ThresholdDown: nil,
			},
			err: nil,
		},
		"fee-too-low": {
			opts: &PaysEnoughOpts{
				UserFee:       common.Big1,
				ExpectedFee:   common.Big2,
				ThresholdUp:   nil,
				ThresholdDown: nil,
			},
			err: ErrFeeTooLow,
		},
		"fee-threshold-down": {
			opts: &PaysEnoughOpts{
				UserFee:       common.Big1,
				ExpectedFee:   common.Big2,
				ThresholdUp:   nil,
				ThresholdDown: new(big.Float).SetFloat64(0.5),
			},
			err: nil,
		},
		"fee-threshold-up": {
			opts: &PaysEnoughOpts{
				UserFee:       common.Big256,
				ExpectedFee:   common.Big1,
				ThresholdUp:   new(big.Float).SetFloat64(1.5),
				ThresholdDown: nil,
			},
			err: ErrFeeTooHigh,
		},
		"fee-too-low-high": {
			opts: &PaysEnoughOpts{
				UserFee:       new(big.Int).SetUint64(10_000),
				ExpectedFee:   new(big.Int).SetUint64(1),
				ThresholdUp:   new(big.Float).SetFloat64(3),
				ThresholdDown: new(big.Float).SetFloat64(0.8),
			},
			err: ErrFeeTooHigh,
		},
		"fee-too-low-down": {
			opts: &PaysEnoughOpts{
				UserFee:       new(big.Int).SetUint64(1),
				ExpectedFee:   new(big.Int).SetUint64(10_000),
				ThresholdUp:   new(big.Float).SetFloat64(3),
				ThresholdDown: new(big.Float).SetFloat64(0.8),
			},
			err: ErrFeeTooLow,
		},
		"fee-too-low-down-2": {
			opts: &PaysEnoughOpts{
				UserFee:       new(big.Int).SetUint64(0),
				ExpectedFee:   new(big.Int).SetUint64(10_000),
				ThresholdUp:   new(big.Float).SetFloat64(3),
				ThresholdDown: new(big.Float).SetFloat64(0.8),
			},
			err: ErrFeeTooLow,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := PaysEnough(tt.opts)
			if !errors.Is(err, tt.err) {
				t.Fatalf("%s: got %s, expected %s", name, err, tt.err)
			}
		})
	}
}
