package processing

// Frozen full-frame implementation from before edge scanning. Keep this independent
// of the optimized traversal: it is the accuracy oracle and benchmark baseline.
func referenceLumaCrop(data []byte, width, height, stride int, is10Bit bool, lumaShift int) (detectedCrop, bool) {
	if width <= 0 || height <= 0 || stride <= 0 {
		return detectedCrop{}, false
	}
	bytesPerSample := 1
	if is10Bit {
		bytesPerSample = 2
	}
	if stride < width*bytesPerSample || len(data) < stride*height {
		return detectedCrop{}, false
	}

	stats := newLumaStats(width, height)
	for row := 0; row < height; row++ {
		rowOff := row * stride
		for col := 0; col < width; col++ {
			stats.add(row, col, readLuma8(data, rowOff, col, is10Bit, lumaShift))
		}
	}
	if stats.activePixels < minActiveFramePixels(width, height) {
		return detectedCrop{}, false
	}

	top, ok := stats.firstActiveRow()
	if !ok {
		return detectedCrop{}, false
	}
	bottom, ok := stats.lastActiveRow()
	if !ok {
		return detectedCrop{}, false
	}
	left, ok := stats.firstActiveCol()
	if !ok {
		return detectedCrop{}, false
	}
	right, ok := stats.lastActiveCol()
	if !ok {
		return detectedCrop{}, false
	}

	return detectedCrop{
		Top:    uint32(top),
		Bottom: uint32(height - 1 - bottom),
		Left:   uint32(left),
		Right:  uint32(width - 1 - right),
	}, true
}

type lumaStats struct {
	width        int
	height       int
	rowCounts    []int
	colCounts    []int
	rowMin       []uint8
	rowMax       []uint8
	colMin       []uint8
	colMax       []uint8
	activePixels int
}

func newLumaStats(width, height int) lumaStats {
	stats := lumaStats{
		width:     width,
		height:    height,
		rowCounts: make([]int, height),
		colCounts: make([]int, width),
		rowMin:    make([]uint8, height),
		rowMax:    make([]uint8, height),
		colMin:    make([]uint8, width),
		colMax:    make([]uint8, width),
	}
	for row := range stats.rowMin {
		stats.rowMin[row] = 255
	}
	for col := range stats.colMin {
		stats.colMin[col] = 255
	}
	return stats
}

func (s *lumaStats) add(row, col int, value uint8) {
	if value < s.rowMin[row] {
		s.rowMin[row] = value
	}
	if value > s.rowMax[row] {
		s.rowMax[row] = value
	}
	if value < s.colMin[col] {
		s.colMin[col] = value
	}
	if value > s.colMax[col] {
		s.colMax[col] = value
	}
	if value > blackLumaThreshold {
		s.rowCounts[row]++
		s.colCounts[col]++
		s.activePixels++
	}
}

func (s *lumaStats) activeRow(row int) bool {
	return s.rowCounts[row] >= minActiveLinePixels(s.width) ||
		(s.rowMax[row]-s.rowMin[row] >= contrastThreshold && s.rowCounts[row] > 0)
}

func (s *lumaStats) activeCol(col int) bool {
	return s.colCounts[col] >= minActiveLinePixels(s.height) ||
		(s.colMax[col]-s.colMin[col] >= contrastThreshold && s.colCounts[col] > 0)
}

func (s *lumaStats) firstActiveRow() (int, bool) {
	for row := 0; row < s.height; row++ {
		if s.activeRow(row) {
			return row, true
		}
	}
	return 0, false
}

func (s *lumaStats) lastActiveRow() (int, bool) {
	for row := s.height - 1; row >= 0; row-- {
		if s.activeRow(row) {
			return row, true
		}
	}
	return 0, false
}

func (s *lumaStats) firstActiveCol() (int, bool) {
	for col := 0; col < s.width; col++ {
		if s.activeCol(col) {
			return col, true
		}
	}
	return 0, false
}

func (s *lumaStats) lastActiveCol() (int, bool) {
	for col := s.width - 1; col >= 0; col-- {
		if s.activeCol(col) {
			return col, true
		}
	}
	return 0, false
}
