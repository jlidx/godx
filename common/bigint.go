// Copyright 2019 DxChain, All rights reserved.
// Use of this source code is governed by an Apache
// License 2.0 that can be found in the LICENSE file.

package common

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
)

// Common used BigInt numbers
var (
	BigInt0 = NewBigInt(0)
	BigInt1 = NewBigInt(1)
)

type BigInt struct {
	b big.Int
}

// NewBigInt will be used to convert the int64 data type into BigInt data type
func NewBigInt(x int64) BigInt {
	return BigInt{
		b: *big.NewInt(x),
	}
}

// NewBigIntUint64 will be used to convert the uint64 data type into BigInt data type
func NewBigIntUint64(x uint64) BigInt {
	return BigInt{
		b: *new(big.Int).SetUint64(x),
	}
}

// NewBigIntFloat64 will be used to convert the float64 data type into BigInt data type
func NewBigIntFloat64(x float64) BigInt {
	v := uint64(x)
	return BigInt{
		b: *new(big.Int).SetUint64(v),
	}
}

// RandomBigIntRange will randomly return a BigInt data based on the range provided
// the input must be greater than 0
func RandomBigIntRange(x BigInt) (random BigInt, err error) {
	if x.IsNeg() || x.IsEqual(BigInt0) {
		return BigInt{}, errors.New("the input range cannot be negative or 0")
	}

	randint, err := rand.Int(rand.Reader, x.BigIntPtr())

	if err != nil {
		return BigInt{}, err
	}
	random = BigInt{
		b: *randint,
	}
	return
}

// RandomBigInt will randomly return a BigInt data between 0-1000
func RandomBigInt() BigInt {
	randint, _ := rand.Int(rand.Reader, big.NewInt(1000))
	return BigInt{
		b: *randint,
	}
}

// String will return the string version of the BigInt
func (x BigInt) String() string {
	return x.b.String()
}

// IsNeg will be used to check if the BigInt is negative
func (x BigInt) IsNeg() bool {
	if x.Cmp(BigInt0) < 0 {
		return true
	}
	return false
}

// IsEqual will be used to indicate if two BigInt data
// are equivalent to each other. Return true if two BigInt
// are equivalent
func (x BigInt) IsEqual(y BigInt) bool {
	if x.Cmp(y) == 0 {
		return true
	}
	return false
}

// Add will perform the addition operation for BigInt data
func (x BigInt) Add(y BigInt) (sum BigInt) {
	sum.b.Add(&x.b, &y.b)
	return
}

// Sub will perform the subtraction operation for BigInt data
func (x BigInt) Sub(y BigInt) (diff BigInt) {
	diff.b.Sub(&x.b, &y.b)
	return
}

// Mult will perform the multiplication operation for BigInt data
func (x BigInt) Mult(y BigInt) (prod BigInt) {
	prod.b.Mul(&x.b, &y.b)
	return
}

// MultInt will perform the multiplication operation between BigInt data and int64 data
func (x BigInt) MultInt(y int64) (prod BigInt) {
	prod.b.Mul(&x.b, big.NewInt(y))
	return
}

// MultUint64 will perform the multiplication operation between BigInt data and uint64 data
func (x BigInt) MultUint64(y uint64) (prod BigInt) {
	prod.b.Mul(&x.b, new(big.Int).SetUint64(y))
	return
}

// MultFloat64 will perform the multiplication operation between BigInt data and float64 data
func (x BigInt) MultFloat64(y float64) (prod BigInt) {
	xRat := new(big.Rat).SetInt(&x.b)
	yRat := new(big.Rat).SetFloat64(y)
	ratProd := new(big.Rat).Mul(xRat, yRat)
	prod.b.Div(ratProd.Num(), ratProd.Denom())
	return
}

// Div will perform the division operation between BigInt data
func (x BigInt) Div(y BigInt) (quotient BigInt) {
	// denominator cannot be 0
	if y.Cmp(NewBigInt(0)) == 0 {
		y = NewBigInt(1)
	}

	// division
	quotient.b.Div(&x.b, &y.b)
	return
}

// DivUint64 will perform the division operation between BigInt data and uint64 data
func (x BigInt) DivUint64(y uint64) (quotient BigInt) {
	quotient.b.Div(&x.b, new(big.Int).SetUint64(y))
	return
}

// Cmp will compare two BigInt Data
// x == y  0
// x > y   1
// x < y  -1
func (x BigInt) Cmp(y BigInt) (result int) {
	result = x.b.Cmp(&y.b)
	return
}

// float64 will convert the BigInt data type into float64 data type
func (x BigInt) Float64() (result float64) {
	f := new(big.Float).SetInt(&x.b)
	result, _ = f.Float64()
	return
}

// BigIntPtr will return the pointer version of the big.Int
func (x BigInt) BigIntPtr() *big.Int {
	return &x.b
}

// PtrBigInt convert the pointer version of big.Int to BigInt type
func PtrBigInt(x *big.Int) (y BigInt) {
	y = BigInt{
		b: *x,
	}

	return
}

// MarshalJSON provided JSON encoding rules for BigInt data type
func (x BigInt) MarshalJSON() ([]byte, error) {
	return []byte(x.b.String()), nil
}

// UnmarshalJSON provided JSON decoding rules for BigInt data type
func (x *BigInt) UnmarshalJSON(val []byte) error {
	if string(val) == "null" {
		return nil
	}
	var y big.Int
	_, ok := y.SetString(string(val), 10)
	if !ok {
		return fmt.Errorf("invalid big integer: %v", y)
	}
	x.b = y
	return nil
}
