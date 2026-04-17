package planets

// Shape is a 6-row × 12-col grid of shade values in [0.0, 1.0] describing
// how a planet renders. Each cell is one terminal character. 0.0 = empty,
// 1.0 = maximum intensity.
type Shape = [6][12]float64

// mercurySimpleSphere — featureless silvery sphere, linear falloff.
var mercurySimpleSphere = Shape{
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.4, 0.0, 0.0},
	{0.0, 0.3, 0.7, 0.9, 1.0, 1.0, 1.0, 0.9, 0.7, 0.5, 0.2, 0.0},
	{0.0, 0.3, 0.7, 0.9, 1.0, 1.0, 1.0, 0.9, 0.7, 0.5, 0.2, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.4, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
}

// venusUniformHaze — thick bright atmosphere, nearly uniform disk.
var venusUniformHaze = Shape{
	{0.0, 0.0, 0.0, 0.4, 0.6, 0.7, 0.7, 0.6, 0.4, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.5, 0.8, 0.9, 0.9, 0.9, 0.9, 0.8, 0.5, 0.0, 0.0},
	{0.0, 0.4, 0.8, 0.9, 0.9, 1.0, 1.0, 0.9, 0.9, 0.7, 0.3, 0.0},
	{0.0, 0.4, 0.8, 0.9, 0.9, 1.0, 1.0, 0.9, 0.9, 0.7, 0.3, 0.0},
	{0.0, 0.0, 0.5, 0.8, 0.9, 0.9, 0.9, 0.9, 0.8, 0.5, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.4, 0.6, 0.7, 0.7, 0.6, 0.4, 0.0, 0.0, 0.0},
}

// earthContinents — irregular shading suggests land/sea contrast.
var earthContinents = Shape{
	{0.0, 0.0, 0.0, 0.3, 0.6, 0.7, 0.7, 0.6, 0.3, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.5, 0.8, 0.7, 0.9, 0.9, 0.8, 0.6, 0.3, 0.0, 0.0},
	{0.0, 0.3, 0.6, 0.7, 0.9, 1.0, 0.8, 0.9, 0.7, 0.4, 0.1, 0.0},
	{0.0, 0.2, 0.7, 0.9, 0.8, 1.0, 1.0, 0.7, 0.8, 0.5, 0.2, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.9, 0.8, 0.9, 0.8, 0.6, 0.3, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
}

// marsPolarCap — top row slightly brighter than bottom, hint of northern ice cap.
var marsPolarCap = Shape{
	{0.0, 0.0, 0.0, 0.3, 0.6, 0.8, 0.8, 0.6, 0.3, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.4, 0.0, 0.0},
	{0.0, 0.3, 0.6, 0.8, 0.9, 1.0, 1.0, 0.9, 0.7, 0.4, 0.1, 0.0},
	{0.0, 0.2, 0.5, 0.8, 0.9, 1.0, 1.0, 0.9, 0.7, 0.4, 0.1, 0.0},
	{0.0, 0.0, 0.3, 0.6, 0.7, 0.8, 0.8, 0.7, 0.5, 0.3, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.2, 0.4, 0.5, 0.5, 0.4, 0.2, 0.0, 0.0, 0.0},
}

// jupiterBands — alternating rows with different core intensity read as horizontal bands.
var jupiterBands = Shape{
	{0.0, 0.0, 0.0, 0.4, 0.6, 0.7, 0.7, 0.6, 0.4, 0.0, 0.0, 0.0},
	{0.0, 0.2, 0.5, 0.8, 1.0, 1.0, 1.0, 0.9, 0.6, 0.2, 0.0, 0.0},
	{0.0, 0.2, 0.6, 0.8, 0.9, 1.0, 1.0, 0.8, 0.7, 0.4, 0.1, 0.0},
	{0.0, 0.1, 0.5, 0.8, 1.0, 1.0, 1.0, 0.9, 0.6, 0.3, 0.0, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.4, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.4, 0.6, 0.7, 0.7, 0.6, 0.4, 0.0, 0.0, 0.0},
}

// saturnRings — sphere in middle 4 rows, ring extends to cols 0-1 and 10-11 on the equator.
var saturnRings = Shape{
	{0.0, 0.0, 0.0, 0.0, 0.4, 0.6, 0.6, 0.4, 0.0, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.2, 0.6, 0.9, 1.0, 1.0, 0.9, 0.6, 0.2, 0.0, 0.0},
	{0.3, 0.4, 0.5, 0.7, 0.9, 1.0, 1.0, 0.9, 0.7, 0.5, 0.4, 0.3},
	{0.3, 0.4, 0.5, 0.7, 0.9, 1.0, 1.0, 0.9, 0.7, 0.5, 0.4, 0.3},
	{0.0, 0.0, 0.2, 0.6, 0.9, 1.0, 1.0, 0.9, 0.6, 0.2, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.0, 0.4, 0.6, 0.6, 0.4, 0.0, 0.0, 0.0, 0.0},
}

// uranusSmallSphere — smaller, uniform disk (Uranus reads as a featureless blue-green ball).
var uranusSmallSphere = Shape{
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.3, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.3, 0.0, 0.0},
	{0.0, 0.2, 0.6, 0.8, 0.9, 1.0, 1.0, 0.9, 0.8, 0.6, 0.2, 0.0},
	{0.0, 0.2, 0.6, 0.8, 0.9, 1.0, 1.0, 0.9, 0.8, 0.6, 0.2, 0.0},
	{0.0, 0.0, 0.3, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.3, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
}

// neptuneDenseCore — smaller, darker-edged disk with bright concentrated core.
var neptuneDenseCore = Shape{
	{0.0, 0.0, 0.0, 0.0, 0.3, 0.5, 0.5, 0.3, 0.0, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.2, 0.5, 0.7, 0.8, 0.8, 0.7, 0.5, 0.2, 0.0, 0.0},
	{0.0, 0.1, 0.5, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.5, 0.1, 0.0},
	{0.0, 0.1, 0.5, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.5, 0.1, 0.0},
	{0.0, 0.0, 0.2, 0.5, 0.7, 0.8, 0.8, 0.7, 0.5, 0.2, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.0, 0.3, 0.5, 0.5, 0.3, 0.0, 0.0, 0.0, 0.0},
}

var shapes = map[string]Shape{
	"mercury": mercurySimpleSphere,
	"venus":   venusUniformHaze,
	"earth":   earthContinents,
	"mars":    marsPolarCap,
	"jupiter": jupiterBands,
	"saturn":  saturnRings,
	"uranus":  uranusSmallSphere,
	"neptune": neptuneDenseCore,
}

// GetShape returns the shade grid for the named planet. Unknown names fall
// back to the mercury sphere so custom instance names still render.
func GetShape(name string) Shape {
	if s, ok := shapes[name]; ok {
		return s
	}
	return mercurySimpleSphere
}
