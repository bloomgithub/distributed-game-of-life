package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"sync"
	"time"

	"uk.ac.bris.cs/gameoflife/util"
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

	World struct {
		Field   Field
		Height  int
		Width   int
		Workers int
	}
)

type (
	BrokerProcessRequest struct {
		Turns int
		World World
	}

	BrokerProcessResponse struct {
		World World
		Turns int
	}

	BrokerReportRequest struct{}

	BrokerReportResponse struct {
		Turns      int
		CellsCount int
	}

	BrokerSaveRequest struct{}

	BrokerSaveResponse struct {
		Turns int
		World World
	}

	BrokerQuitRequest struct{}

	BrokerQuitResponse struct {
		Turns int
	}

	BrokerShutdownRequest struct{}

	BrokerShutdownResponse struct {
		Turns int
	}

	BrokerPauseRequest struct{}

	BrokerPauseResponse struct {
		Turns    int
		IsPaused bool
	}

	BrokerService struct {
		Turns       int
		CellsCount  int
		World       World
		quit        chan bool
		shutdown    chan bool
		pause       chan bool
		isPaused    bool
		workerAddrs []string
	}
)

type (
	WorkerProcessResponse struct {
		Region Region
	}

	WorkerProcessRequest struct {
		Region Region
	}

	WorkerShutdownResponse struct{}

	WorkerShutdownRequest struct{}
)

var WorkerProcess = "WorkerService.Process"

var WorkerShutdown = "WorkerService.Shutdown"

func (region *Region) update(ipAddress string, regionCh chan<- [][]Cell) {
	client, err := rpc.Dial("tcp", ipAddress)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer client.Close()

	request := WorkerProcessRequest{Region: *region}
	response := new(WorkerProcessResponse)
	client.Call(WorkerProcess, request, response)

	regionCh <- response.Region.Field
}

func (world *World) region(w int) Region {
	field := Field{
		Height: 0,
		Width:  0,
	}
	regionHeight := world.Height / world.Workers
	start := w * regionHeight
	end := (w + 1) * regionHeight
	if w == world.Workers-1 {
		end = world.Height
	}
	regionHeight = end - start

	downRowPtr := end % world.Height
	upRowPtr := (start - 1 + world.Height) % world.Height

	field.Data = make([][]Cell, regionHeight+2)
	field.Data[0] = world.Field.Data[upRowPtr]
	for row := 1; row <= regionHeight; row++ {
		field.Data[row] = world.Field.Data[start+row-1]
	}
	field.Data[regionHeight+1] = world.Field.Data[downRowPtr]

	return Region{
		Field:  field.Data,
		Start:  start,
		End:    end,
		Height: regionHeight,
		Width:  world.Width,
	}
}

func (world *World) update(workerAddrs []string) {
	var newFieldData [][]Cell

	regionChannel := make([]chan [][]Cell, world.Workers)

	var wg sync.WaitGroup
	wg.Add(world.Workers)

	for workerID := 0; workerID < world.Workers; workerID++ {
		regionChannel[workerID] = make(chan [][]Cell)
		region := world.region(workerID)
		go func(workerID int) {
			defer func() {
				close(regionChannel[workerID])
				wg.Done()
			}()
			region.update(workerAddrs[workerID], regionChannel[workerID])
		}(workerID)
	}

	for w := 0; w < world.Workers; w++ {
		region := <-regionChannel[w]
		newFieldData = append(newFieldData, region...)
	}

	world.Field.Data = newFieldData
}

func aliveCellsInRow(row []Cell, y int) []util.Cell {
	var alive []util.Cell
	for x, cell := range row {
		if cell.Alive {
			alive = append(alive, util.Cell{X: x, Y: y})
		}
	}
	return alive
}

func (world *World) alive() []util.Cell {
	var alive []util.Cell
	for y, row := range world.Field.Data {
		alive = append(alive, aliveCellsInRow(row, y)...)
	}
	return alive
}

func (b *BrokerService) Report(req BrokerReportRequest, res *BrokerReportResponse) (err error) {
	res.Turns = b.Turns
	res.CellsCount = b.CellsCount
	return
}

func (b *BrokerService) Process(req BrokerProcessRequest, res *BrokerProcessResponse) (err error) {
	turns := req.Turns
	world := req.World

	turn := 0

	for turn < turns {
		select {
		case isPaused := <-b.pause:
			b.isPaused = isPaused
			if b.isPaused {
				// Paused, wait for the signal to resume
				<-b.pause
			}
		case <-b.quit:
			// Received stop signal, exit the loop
			return nil
		default:
			if !b.isPaused {

				world.update(b.workerAddrs)

				b.Turns++
				b.CellsCount = len(world.alive())
				b.World = world

				turn++
			}
		}
	}

	res.World = world
	res.Turns = b.Turns

	return nil
}

func (b *BrokerService) Save(req BrokerSaveRequest, res *BrokerSaveResponse) (err error) {
	res.Turns = b.Turns
	res.World = b.World
	return
}

func (b *BrokerService) Quit(req BrokerQuitRequest, res *BrokerQuitResponse) (err error) {
	res.Turns = b.Turns

	b.Turns = 0
	b.CellsCount = 0
	b.World = World{}

	b.quit <- true

	return nil
}

func (b *BrokerService) Shutdown(req BrokerShutdownRequest, res *BrokerShutdownResponse) (err error) {
	ipAddresses := []string{"18.234.31.207:8030"}

	for _, ipAddress := range ipAddresses {
		client, err := rpc.Dial("tcp", ipAddress)
		if err != nil {
			log.Fatal("dialing:", err)
		}

		defer client.Close()

		request := WorkerShutdownRequest{}
		response := new(WorkerShutdownResponse)
		client.Call(WorkerShutdown, request, response)
	}

	b.shutdown <- true

	res.Turns = b.Turns
	return nil
}

func (b *BrokerService) Pause(req BrokerPauseRequest, res *BrokerPauseResponse) (err error) {
	b.isPaused = !b.isPaused
	b.pause <- b.isPaused
	res.IsPaused = b.isPaused
	res.Turns = b.Turns
	return
}

func main() {
	// TODO: Error handling
	pAddr := flag.String("port", "8030", "Port to listen on")
	flag.Parse()

	b := &BrokerService{
		quit:     make(chan bool),
		shutdown: make(chan bool),
		pause:    make(chan bool),
		isPaused: false,
	}

	rpc.Register(b)

	listener, _ := net.Listen("tcp", ":"+*pAddr)
	defer listener.Close()

	wg := sync.WaitGroup{}
	wg.Add(1)

	go rpc.Accept(listener)

	// Wait for shutdown
	<-b.shutdown

	// Shutdown logic here
	fmt.Println("Shutting down")
	listener.Close()
}
