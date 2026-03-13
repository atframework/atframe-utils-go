package libatframe_utils_lang_utility

import "math/rand"

func IsDeduplicate[T comparable](elem []T) bool {
	seen := make(map[T]struct{})
	for _, v := range elem {
		if _, ok := seen[v]; ok {
			return true
		}
		seen[v] = struct{}{}
	}
	return false
}

func IsDeduplicateWithOut[T comparable](elem []T, witchOut T) bool {
	seen := make(map[T]struct{})
	for _, v := range elem {
		if v == witchOut {
			continue
		}

		if _, ok := seen[v]; ok {
			return true
		}
		seen[v] = struct{}{}
	}
	return false
}

func IsExist[T comparable](elem []T, target T) bool {
	if elem == nil {
		return false
	}

	for _, v := range elem {
		if v == target {
			return true
		}
	}
	return false
}

type WeightRandomElem interface {
	GetWeight() int32
	GetTypeId() int32
}

func WeightRandom[T WeightRandomElem](elem []T, seed int64) (T, bool) {
	return WeightRandomWithAdjustment(elem, seed, nil)
}

func WeightRandomWithAdjustment[T WeightRandomElem](elem []T, seed int64, adjustmentuWeightFunc func(typeId int32, weight int32) int32) (T, bool) {
	var zero T
	if len(elem) == 0 {
		return zero, false
	}

	if adjustmentuWeightFunc == nil {
		adjustmentuWeightFunc = func(typeId int32, weight int32) int32 {
			return weight
		}
	}

	totalWeight := int32(0)
	for _, v := range elem {
		if weight := adjustmentuWeightFunc(v.GetTypeId(), v.GetWeight()); weight > 0 {
			totalWeight += weight
		}
	}

	if totalWeight <= 0 {
		return zero, false
	}

	r := rand.New(rand.NewSource(seed))
	randomWeight := r.Int31n(totalWeight) + 1

	currentWeight := int32(0)
	for _, v := range elem {
		weight := adjustmentuWeightFunc(v.GetTypeId(), v.GetWeight())
		if weight <= 0 {
			continue
		}

		currentWeight += weight
		if randomWeight <= currentWeight {
			return v, true
		}
	}

	return zero, false
}
