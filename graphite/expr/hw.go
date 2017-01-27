package expr

// This holt-winters code copied from graphite's functions.py)
// It's "mostly" the same as a standard HW forecast

import (
	"math"
)

func holtWintersIntercept(alpha, actual, lastSeason, lastIntercept, lastSlope float64) float64 {
	return alpha*(actual-lastSeason) + (1-alpha)*(lastIntercept+lastSlope)
}

func holtWintersSlope(beta, intercept, lastIntercept, lastSlope float64) float64 {
	return beta*(intercept-lastIntercept) + (1-beta)*lastSlope
}

func holtWintersSeasonal(gamma, actual, intercept, lastSeason float64) float64 {
	return gamma*(actual-intercept) + (1-gamma)*lastSeason
}

func holtWintersDeviation(gamma, actual, prediction, lastSeasonalDev float64) float64 {
	if math.IsNaN(prediction) {
		prediction = 0
	}
	return gamma*math.Abs(actual-prediction) + (1-gamma)*lastSeasonalDev
}

func holtWintersAnalysis(series []float64, step int32) ([]float64, []float64) {
	const (
		alpha = 0.1
		beta  = 0.0035
		gamma = 0.1
	)

	// season is currently one day
	seasonLength := 24 * 60 * 60 / int(step)

	var (
		intercepts  []float64
		slopes      []float64
		seasonals   []float64
		predictions []float64
		deviations  []float64
	)

	getLastSeasonal := func(i int) float64 {
		j := i - seasonLength
		if j >= 0 {
			return seasonals[j]
		}
		return 0
	}

	getLastDeviation := func(i int) float64 {
		j := i - seasonLength
		if j >= 0 {
			return deviations[j]
		}
		return 0
	}

	var nextPred = math.NaN()

	for i, actual := range series {
		if math.IsNaN(actual) {
			// missing input values break all the math
			// do the best we can and move on
			intercepts = append(intercepts, math.NaN())
			slopes = append(slopes, 0)
			seasonals = append(seasonals, 0)
			predictions = append(predictions, nextPred)
			deviations = append(deviations, 0)
			nextPred = math.NaN()
			continue
		}

		var (
			lastSlope     float64
			lastIntercept float64
			prediction    float64
		)
		if i == 0 {
			lastIntercept = actual
			lastSlope = 0
			// seed the first prediction as the first actual
			prediction = actual
		} else {
			lastIntercept = intercepts[len(intercepts)-1]
			lastSlope = slopes[len(slopes)-1]
			if math.IsNaN(lastIntercept) {
				lastIntercept = actual
			}
			prediction = nextPred
		}

		lastSeasonal := getLastSeasonal(i)
		nextLastSeasonal := getLastSeasonal(i + 1)
		lastSeasonalDev := getLastDeviation(i)

		intercept := holtWintersIntercept(alpha, actual, lastSeasonal, lastIntercept, lastSlope)
		slope := holtWintersSlope(beta, intercept, lastIntercept, lastSlope)
		seasonal := holtWintersSeasonal(gamma, actual, intercept, lastSeasonal)
		nextPred = intercept + slope + nextLastSeasonal
		deviation := holtWintersDeviation(gamma, actual, prediction, lastSeasonalDev)

		intercepts = append(intercepts, intercept)
		slopes = append(slopes, slope)
		seasonals = append(seasonals, seasonal)
		predictions = append(predictions, prediction)
		deviations = append(deviations, deviation)
	}

	return predictions, deviations
}

func holtWintersConfidenceBands(series []float64, step int32, delta float64) ([]float64, []float64) {
	var lowerBand, upperBand []float64

	predictions, deviations := holtWintersAnalysis(series, step)

	windowPoints := 7 * 86400 / step

	predictionsOfInterest := predictions[windowPoints:]
	deviationsOfInterest := deviations[windowPoints:]

	for i, _ := range predictionsOfInterest {
		if math.IsNaN(predictionsOfInterest[i]) || math.IsNaN(deviationsOfInterest[i]) {
			lowerBand = append(lowerBand, math.NaN())
			upperBand = append(upperBand, math.NaN())
		} else {
			scaledDeviation := delta * deviationsOfInterest[i]
			lowerBand = append(lowerBand, predictionsOfInterest[i]-scaledDeviation)
			upperBand = append(upperBand, predictionsOfInterest[i]+scaledDeviation)
		}
	}

	return lowerBand, upperBand
}
