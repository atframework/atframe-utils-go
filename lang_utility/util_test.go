// Copyright 2026 atframework
package libatframe_utils_lang_utility

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type testWeightElem struct {
	weight int32
	typeID int32
}

func (e testWeightElem) GetWeight() int32 {
	return e.weight
}

func (e testWeightElem) GetTypeId() int32 {
	return e.typeID
}

func TestWeightRandom_EmptyElem_ReturnFalse(t *testing.T) {
	// Arrange
	var elem []testWeightElem

	// Act
	got, ok := WeightRandom(elem, 1)

	// Assert
	assert.False(t, ok)
	assert.Equal(t, testWeightElem{}, got)
}

func TestWeightRandom_AllNonPositiveWeight_ReturnFalse(t *testing.T) {
	// Arrange
	elem := []testWeightElem{{weight: 0, typeID: 1}, {weight: -2, typeID: 2}}

	// Act
	got, ok := WeightRandom(elem, 1)

	// Assert
	assert.False(t, ok)
	assert.Equal(t, testWeightElem{}, got)
}

func TestWeightRandom_SameSeed_ReturnSameResult(t *testing.T) {
	// Arrange
	elem := []testWeightElem{{weight: 1, typeID: 1}, {weight: 3, typeID: 2}, {weight: 6, typeID: 3}}
	seed := int64(123456)

	// Act
	got1, ok1 := WeightRandom(elem, seed)
	got2, ok2 := WeightRandom(elem, seed)

	// Assert
	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, got1.GetTypeId(), got2.GetTypeId())
}

func TestWeightRandom_NilAdjustment_Equivalent(t *testing.T) {
	// Arrange
	elem := []testWeightElem{{weight: 1, typeID: 1}, {weight: 3, typeID: 2}, {weight: 6, typeID: 3}}
	seed := int64(20260310)

	// Act
	got1, ok1 := WeightRandom(elem, seed)
	got2, ok2 := WeightRandomWithAdjustment(elem, seed, nil)

	// Assert
	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, got1.GetTypeId(), got2.GetTypeId())
}

func TestWeightRandomWithAdjustment_AdjustWeightApplied(t *testing.T) {
	// Arrange
	elem := []testWeightElem{{weight: 1, typeID: 1}, {weight: 3, typeID: 2}}
	seed := int64(7)
	adjustmentWeightFunc := func(typeId int32, weight int32) int32 {
		if typeId == 2 {
			return 0
		}

		return weight
	}

	// Act
	got, ok := WeightRandomWithAdjustment(elem, seed, adjustmentWeightFunc)

	// Assert
	assert.True(t, ok)
	assert.Equal(t, int32(1), got.GetTypeId())
}
