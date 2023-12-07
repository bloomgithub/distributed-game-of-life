package main

import (
	"flag"
	"net"
	"net/rpc"
	"time"
)

const (
	DefaultHaloOffset = 1
	InitialDelay      = 2 * time.Second
)

type (
	Cell struct {
		X     int
		Y     int
		Alive bool
	}

	Field struct {
		Data   [][]Cell
		Height int
		Width  int
	}

	Region struct {
		Field  [][]Cell
		Start  int
		End    int
		Height int
		Width  int
	}
)

type (
	WorkerProcessRequest struct {
		Region Region
	}

	WorkerProcessResponse struct {
		Region Region
	}

	WorkerShutdownRequest struct{}

	WorkerShutdownResponse struct{}

	WorkerService struct {
		shutdown chan bool
	}
)

func (field *Field) cultivate(height, width int) Field {
	land := make([][]Cell, height)
	for i := range land {
		land[i] = make([]Cell, width)
	}
	field.Data = land
	return *field
}

func (region *Region) update() {
	field := Field{
		Height: region.Height,
		Width:  region.Width,
	}
	field.cultivate(region.Height, region.Width)

	for y := DefaultHaloOffset; y < region.Height+DefaultHaloOffset; y++ {
		for x := 0; x < region.Width; x++ {
			currentCell := region.Field[y][x]
			nextCell := currentCell
			aliveNeighbours := 0
			for i := -1; i <= 1; i++ {
				for j := -1; j <= 1; j++ {
					wx := x + i
					wy := y + j
					wx += region.Width
					wx %= region.Width
					if (j != 0 || i != 0) && region.Field[wy][wx].Alive {
						aliveNeighbours++
					}
				}
			}
			if (aliveNeighbours < 2) || (aliveNeighbours > 3) {
				nextCell.Alive = false
			}
			if aliveNeighbours == 3 {
				nextCell.Alive = true
			}
			field.Data[y-DefaultHaloOffset][x] = nextCell
		}
	}

	region.Field = field.Data
}

func (w *WorkerService) Process(req WorkerProcessRequest, res *WorkerProcessResponse) (err error) {
	region := req.Region

	region.update()
	res.Region = region
	return
}

func (w *WorkerService) Shutdown(req WorkerProcessRequest, res *WorkerProcessResponse) (err error) {
	w.shutdown <- true
	return nil
}

func main() {
	// TODO: Error handling
	pAddr := flag.String("port", "8030", "Port to listen on")
	flag.Parse()

	w := &WorkerService{
		shutdown: make(chan bool),
	}

	rpc.Register(w)
	listener, _ := net.Listen("tcp", ":"+*pAddr)
	defer listener.Close()
	go rpc.Accept(listener)

	// Wait for shutdown
	<-w.shutdown

	// Shutdown logic here
	listener.Close()
}
