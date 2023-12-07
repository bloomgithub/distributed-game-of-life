package gol

import (
	"fmt"
	"log"
	"net/rpc"
	"time"

	"uk.ac.bris.cs/gameoflife/util"
)

const (
	DefaultHaloOffset = 1
	InitialDelay      = 2 * time.Second
)

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
	keyPresses <-chan rune
}

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

	World struct {
		Field  Field
		Height int
		Width  int
	}
)

type Reporter struct {
	EventsCh       chan<- Event
	ReportInterval time.Duration
	Stop           chan bool
}

type (
	BrokerProcessRequest struct {
		Turns int
		World World
	}

	BrokerProcessResponse struct {
		World World
		Turns int
	}

	BrokerReportResponse struct {
		Turns      int
		CellsCount int
		World      World
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

	BrokerReportRequest struct{}

	BrokerShutdownResponse struct {
		Turns int
	}

	BrokerShutdownRequest struct{}

	BrokerPauseRequest struct{}

	BrokerPauseResponse struct {
		Turns    int
		IsPaused bool
	}

	BrokerService struct {
		Turns      int
		CellsCount int
		World      World
	}
)

var BrokerProcess = "BrokerService.Process"

var BrokerReport = "BrokerService.Report"

var BrokerSave = "BrokerService.Save"

var BrokerQuit = "BrokerService.Quit"

var BrokerShutdown = "BrokerService.Shutdown"

var BrokerPause = "BrokerService.Pause"

func (field *Field) cultivate(height, width int) Field {
	land := make([][]Cell, height)
	for i := range land {
		land[i] = make([]Cell, width)
	}
	field.Data = land
	return *field
}

func (world *World) populate(c distributorChannels) {
	flipped := []util.Cell{}
	for y := 0; y < world.Height; y++ {
		for x := 0; x < world.Width; x++ {
			cell := <-c.ioInput
			world.Field.Data[y][x] = Cell{X: x, Y: y, Alive: cell == 255}
			if cell == 255 {
				c.events <- CellFlipped{0, util.Cell{X: x, Y: y}}
				flipped = append(flipped, util.Cell{X: x, Y: y})
			}
		}
	}
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

func (reporter *Reporter) start(client *rpc.Client) {
	initialDelay := time.After(InitialDelay)
	ticker := time.NewTicker(reporter.ReportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-initialDelay:
			// Initial delay elapsed, start reporting
		case <-ticker.C:
			request := BrokerReportRequest{}
			response := new(BrokerReportResponse)
			client.Call(BrokerReport, request, response)
			turns := response.Turns
			cellsCount := response.CellsCount
			// log.Printf("Turns: %d, Alive Cells: %d\n", turns, cellsCount)
			reporter.EventsCh <- AliveCellsCount{
				CompletedTurns: turns,
				CellsCount:     cellsCount,
			}
		case <-reporter.Stop:
			// Stop signal received, exit the loop
			return
		}
	}
}

func generateFilename(world *World, turn int) string {
	return fmt.Sprintf("%vx%vx%v", world.Width, world.Width, turn)
}

func saveWorldToFile(world *World, c distributorChannels) {
	for y := 0; y < world.Height; y++ {
		for x := 0; x < world.Width; x++ {
			var aliveValue uint8
			if world.Field.Data[y][x].Alive {
				aliveValue = 255
			}
			c.ioOutput <- aliveValue
		}
	}
}

func (world *World) save(turn int, c distributorChannels) {
	filename := generateFilename(world, turn)
	c.ioCommand <- ioOutput
	c.ioFilename <- filename
	saveWorldToFile(world, c)
	c.events <- ImageOutputComplete{
		CompletedTurns: turn,
		Filename:       filename,
	}
}

func distributor(p Params, c distributorChannels) {
	filename := fmt.Sprintf("%vx%v", p.ImageWidth, p.ImageHeight)

	c.ioCommand <- ioInput

	c.ioFilename <- filename

	field := Field{
		Height: p.ImageHeight,
		Width:  p.ImageWidth,
	}
	field.cultivate(p.ImageHeight, p.ImageWidth)

	world := World{
		Field:  field,
		Height: p.ImageHeight,
		Width:  p.ImageWidth,
	}
	world.populate(c)

	reporter := Reporter{
		EventsCh:       c.events,
		ReportInterval: InitialDelay,
		Stop:           make(chan bool),
	}

	brokerAddr := "127.0.0.1:8030"

	client, err := rpc.Dial("tcp", brokerAddr)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer client.Close()

	go reporter.start(client)

	go func() {
		for {
			select {
			case key := <-c.keyPresses:
				if key == 's' {
					saveRequest := BrokerSaveRequest{}
					saveResponse := new(BrokerSaveResponse)
					client.Call(BrokerReport, saveRequest, saveResponse)
					saveResponse.World.save(saveResponse.Turns, c)
				} else if key == 'q' {
					quitRequest := BrokerQuitRequest{}
					quitResponse := new(BrokerQuitResponse)
					client.Call(BrokerQuit, quitRequest, quitResponse)
					c.events <- StateChange{
						CompletedTurns: quitResponse.Turns,
						NewState:       Quitting,
					}

					return
				} else if key == 'k' {
					shutdownRequest := BrokerShutdownRequest{}
					shutdownResponse := new(BrokerShutdownResponse)
					client.Call(BrokerShutdown, shutdownRequest, shutdownResponse)
					c.events <- StateChange{
						CompletedTurns: shutdownResponse.Turns,
						NewState:       Quitting,
					}
					return
				} else if key == 'p' {
					pauseRequest := BrokerPauseRequest{}
					pauseResponse := new(BrokerPauseResponse)
					client.Call(BrokerPause, pauseRequest, pauseResponse)
					if pauseResponse.IsPaused {
						c.events <- StateChange{
							CompletedTurns: pauseResponse.Turns,
							NewState:       Paused,
						}
					} else {
						c.events <- StateChange{
							CompletedTurns: pauseResponse.Turns,
							NewState:       Executing,
						}
					}
				}
			}
		}
	}()

	processRequest := BrokerProcessRequest{
		World: world,
		Turns: p.Turns,
	}

	processResponse := new(BrokerProcessResponse)

	client.Call(BrokerProcess, processRequest, processResponse)

	world = processResponse.World

	reporter.Stop <- true

	c.events <- FinalTurnComplete{
		CompletedTurns: p.Turns,
		Alive:          world.alive(),
	}

	world.save(p.Turns, c)

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{
		CompletedTurns: p.Turns,
		NewState:       Quitting,
	}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
