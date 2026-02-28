package trace

import (
	"container/heap"
	"image"
	"math"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// FindPathOnSkeleton finds a path on a skeletonized binary mask between two points
// using A* with 8-connected neighbors and Euclidean heuristic.
// Returns the pixel-coordinate path and true, or nil and false if unreachable.
func FindPathOnSkeleton(skeleton gocv.Mat, start, end geometry.Point2D, maxSearchRadius int) ([]geometry.Point2D, bool) {
	if skeleton.Empty() {
		return nil, false
	}

	// Find nearest skeleton pixels to start and end
	startPt, ok := NearestSkeletonPixel(skeleton, start, maxSearchRadius)
	if !ok {
		return nil, false
	}
	endPt, ok := NearestSkeletonPixel(skeleton, end, maxSearchRadius)
	if !ok {
		return nil, false
	}

	rows, cols := skeleton.Rows(), skeleton.Cols()

	// A* search
	type node struct {
		x, y int
	}

	startNode := node{startPt.X, startPt.Y}
	endNode := node{endPt.X, endPt.Y}

	if startNode == endNode {
		return []geometry.Point2D{
			{X: float64(startPt.X), Y: float64(startPt.Y)},
		}, true
	}

	// g-score: cost from start to this node
	gScore := make(map[node]float64)
	gScore[startNode] = 0

	// came-from: for path reconstruction
	cameFrom := make(map[node]node)

	// Priority queue
	pq := &pathQueue{}
	heap.Init(pq)
	heap.Push(pq, &pathItem{
		x: startNode.x, y: startNode.y,
		f: euclidean(startNode.x, startNode.y, endNode.x, endNode.y),
	})

	// 8-connected neighbors
	dx := [8]int{-1, 0, 1, -1, 1, -1, 0, 1}
	dy := [8]int{-1, -1, -1, 0, 0, 1, 1, 1}
	cost := [8]float64{math.Sqrt2, 1, math.Sqrt2, 1, 1, math.Sqrt2, 1, math.Sqrt2}

	visited := make(map[node]bool)

	for pq.Len() > 0 {
		item := heap.Pop(pq).(*pathItem)
		cur := node{item.x, item.y}

		if cur == endNode {
			// Reconstruct path
			var path []geometry.Point2D
			n := endNode
			for {
				path = append(path, geometry.Point2D{X: float64(n.x), Y: float64(n.y)})
				prev, ok := cameFrom[n]
				if !ok {
					break
				}
				n = prev
			}
			// Reverse
			for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
				path[i], path[j] = path[j], path[i]
			}
			return path, true
		}

		if visited[cur] {
			continue
		}
		visited[cur] = true

		curG := gScore[cur]

		for d := 0; d < 8; d++ {
			nx, ny := cur.x+dx[d], cur.y+dy[d]
			if nx < 0 || nx >= cols || ny < 0 || ny >= rows {
				continue
			}
			if skeleton.GetUCharAt(ny, nx) == 0 {
				continue
			}

			neighbor := node{nx, ny}
			if visited[neighbor] {
				continue
			}

			tentativeG := curG + cost[d]
			prevG, exists := gScore[neighbor]
			if !exists || tentativeG < prevG {
				gScore[neighbor] = tentativeG
				cameFrom[neighbor] = cur
				f := tentativeG + euclidean(nx, ny, endNode.x, endNode.y)
				heap.Push(pq, &pathItem{x: nx, y: ny, f: f})
			}
		}
	}

	return nil, false
}

// NearestSkeletonPixel finds the nearest white pixel on the skeleton mask
// to the given point, scanning outward in a spiral pattern up to maxRadius.
func NearestSkeletonPixel(skeleton gocv.Mat, pt geometry.Point2D, maxRadius int) (image.Point, bool) {
	cx, cy := int(math.Round(pt.X)), int(math.Round(pt.Y))
	rows, cols := skeleton.Rows(), skeleton.Cols()

	// Check center first
	if cx >= 0 && cx < cols && cy >= 0 && cy < rows {
		if skeleton.GetUCharAt(cy, cx) > 0 {
			return image.Point{X: cx, Y: cy}, true
		}
	}

	// Scan outward in increasing radius
	bestDist := float64(maxRadius + 1)
	var bestPt image.Point
	found := false

	for r := 1; r <= maxRadius; r++ {
		// Scan the border of the square at distance r
		for dx := -r; dx <= r; dx++ {
			for _, dy := range []int{-r, r} {
				x, y := cx+dx, cy+dy
				if x < 0 || x >= cols || y < 0 || y >= rows {
					continue
				}
				if skeleton.GetUCharAt(y, x) > 0 {
					d := math.Sqrt(float64(dx*dx + dy*dy))
					if d < bestDist {
						bestDist = d
						bestPt = image.Point{X: x, Y: y}
						found = true
					}
				}
			}
		}
		for dy := -r + 1; dy <= r-1; dy++ {
			for _, dx := range []int{-r, r} {
				x, y := cx+dx, cy+dy
				if x < 0 || x >= cols || y < 0 || y >= rows {
					continue
				}
				if skeleton.GetUCharAt(y, x) > 0 {
					d := math.Sqrt(float64(dx*dx + dy*dy))
					if d < bestDist {
						bestDist = d
						bestPt = image.Point{X: x, Y: y}
						found = true
					}
				}
			}
		}
		// If we found something at this radius, no need to go further
		if found {
			return bestPt, true
		}
	}

	return image.Point{}, false
}

func euclidean(x1, y1, x2, y2 int) float64 {
	dx := float64(x2 - x1)
	dy := float64(y2 - y1)
	return math.Sqrt(dx*dx + dy*dy)
}

// pathItem is a node in the A* priority queue.
type pathItem struct {
	x, y  int
	f     float64
	index int
}

// pathQueue implements heap.Interface for A* search.
type pathQueue []*pathItem

func (pq pathQueue) Len() int           { return len(pq) }
func (pq pathQueue) Less(i, j int) bool { return pq[i].f < pq[j].f }
func (pq pathQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *pathQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*pathItem)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *pathQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[:n-1]
	return item
}
