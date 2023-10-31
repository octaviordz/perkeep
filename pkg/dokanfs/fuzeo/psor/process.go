package psor // import "perkeep.org/pkg/dokanfs/fuzeo/psor"

import (
	"container/ring"
	"fmt"
)

func printf(format string, a ...any) (n int, err error) {
	return fmt.Printf(format, a...)
}

type ProcessArg interface {
	IsProcessArg()
}

type ProcessState = uint8

// const (
// 	processStatePending = iota
// 	processStateSettled
// )

type Dispatch = func(note any)

type Effect = func(dispatch Dispatch)

type Cmd = []Effect

var CmdNone = []Effect{}

func CmdOfNote(note any) Cmd {
	f := func(dispatch Dispatch) {
		dispatch(note)
	}
	return []Effect{f}
}

func BatchCmd(cmds []Cmd) Cmd {
	var result Cmd = make(Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		result = append(result, cmd...)
	}
	return result
}

func execCmd(dispatch Dispatch, cmd Cmd) {
	if cmd == nil {
		return
	}
	for _, call := range cmd {
		if call == nil || dispatch == nil {
			continue
		}
		call(dispatch)
	}
}

type Unsubscriber = func()

type SubId = uint8

type Subscribe = func(dispatch Dispatch) Unsubscriber

type Subent struct {
	SubId
	Subscribe
}

type Sub = []Subent

type Subedent struct {
	SubId
	Unsubscriber
}

type diffSubsResult struct {
	dupes   subIdSet
	toStop  []Subedent
	toKeep  []Subedent
	toStart []Subent
}

type subIdSet map[SubId]bool

func newSubIdSet() subIdSet {
	return make(subIdSet)
}

func (s subIdSet) add(item SubId) {
	s[item] = true
}

func (s subIdSet) remove(item SubId) {
	delete(s, item)
}

func (s subIdSet) contains(item SubId) bool {
	return s[item]
}

func (s subIdSet) equals(su subIdSet) bool {
	if len(s) != len(su) {
		return false
	}

	for key := range s {
		if !su.contains(key) {
			return false
		}
	}

	return true
}

func diffSubs(activeSubs []Subedent, sub Sub) (r diffSubsResult) {
	keys := newSubIdSet()
	for _, subedent := range activeSubs {
		keys.add(subedent.SubId)
	}
	// let dupes, newKeys, newSubs = NewSubs.calculate sub
	dupes := newSubIdSet()
	newKeys := newSubIdSet()
	newSubs := []Subent{}
	for _, s := range sub {
		if newKeys.contains(s.SubId) {
			dupes.add(s.SubId)
		} else {
			newKeys.add(s.SubId)
			newSubs = append(newSubs, s)
		}
	}
	// if keys = newKeys then
	if keys.equals(newKeys) {
		r = diffSubsResult{
			dupes:   dupes,
			toStop:  []Subedent{},
			toKeep:  activeSubs,
			toStart: []Subent{},
		}
	} else {
		toKeep := []Subedent{}
		toStop := []Subedent{}
		for _, s := range activeSubs {
			if newKeys.contains(s.SubId) {
				toKeep = append(toKeep, s)
			} else {
				toStop = append(toStop, s)
			}
		}
		toStart := []Subent{}
		for _, s := range newSubs {
			if !keys.contains(s.SubId) {
				toStart = append(toStart, s)
			}
		}
		r = diffSubsResult{
			dupes:   dupes,
			toStop:  toStop,
			toKeep:  toKeep,
			toStart: toStart,
		}
	}
	return
}

func changeSubs(dispatch Dispatch, diffResult diffSubsResult) []Subedent {
	r := []Subedent{}
	for _, subent := range diffResult.toStart {
		id := subent.SubId
		start := subent.Subscribe
		unsub := start(dispatch)
		r = append(r, Subedent{id, unsub})
	}
	return r
}

type Processor[Tdata any] struct {
	Process *Process[Tdata]
}

type RingBuffer struct {
	h *ring.Ring
	r *ring.Ring
}

func NewRingBuffer() *RingBuffer {
	r := ring.New(1)
	return &RingBuffer{
		h: r,
		r: r,
	}
}

func (x *RingBuffer) Push(v any) {
	x.r.Value = v
	s := ring.New(1)
	x.r.Link(s)
	x.r = s
}

func (x *RingBuffer) Pop() any {
	if x.h != x.r {
		rl := x.r.Unlink(1)
		// debugf("ring buffer length %d", rl.Len())
		x.h = x.r.Next()
		return rl.Value
	}
	return nil
}

func (x *RingBuffer) Peek() any {
	if x.h != x.r {
		v := x.h.Value
		return v
	}
	return nil
}

func (p *Processor[Tdata]) Step(arg ProcessArg) {
	p.Process.UpdateWith(arg)
}

func (p *Processor[Tdata]) Fetch() Tdata {
	return p.Process.GetData()
}

func (p *Processor[Tdata]) State() ProcessState {
	return p.Process.GetProcessState()
}

type Process[Tdata any] struct {
	Init            func(ProcessArg) (Tdata, Cmd)
	PutData         func(Tdata)
	GetData         func() Tdata
	GetProcessState func() ProcessState
	Update          func(note any, data Tdata) (Tdata, Cmd)
	UpdateWith      func(ProcessArg)
	Subscribe       func(data Tdata) Sub
}

// / Trace all the updates to the console
// let withConsoleTrace (program: Program<'arg, 'model, 'msg, 'view>) =
func (p *Processor[Tdata]) WithConsoleLog() *Processor[Tdata] {
	// let traceInit (arg:'arg) =
	traceInit := func(arg ProcessArg) (Tdata, Cmd) {
		// let initModel,cmd = program.init arg
		initData, cmd := p.Process.Init(arg)
		// Log.toConsole ("Initial state:", initModel)
		printf("Initial data: %v\n", initData)
		// initModel,cmd
		return initData, cmd
	}

	// let traceUpdate msg model =
	traceUpdate := func(n any, data Tdata) (Tdata, Cmd) {
		// Log.toConsole ("New message:", msg)
		printf("New note: %v\n", n)
		// let newModel,cmd = program.update msg model
		newData, cmd := p.Process.Update(n, data)
		// Log.toConsole ("Updated state:", newModel)
		printf("Updated state: %v\n", newData)
		// newModel,cmd
		return newData, cmd
	}

	// let traceSubscribe model =
	traceSubscribe := func(data Tdata) Sub {
		// let sub = program.subscribe model
		sub := p.Process.Subscribe(data)
		// Log.toConsole ("Updated subs:", sub |> List.map fst)
		subId := make([]SubId, 0, len(sub))
		for _, it := range sub {
			subId = append(subId, it.SubId)
		}
		printf("Updated subs: %v\n", subId)
		// sub
		return sub
	}

	return &Processor[Tdata]{
		Process: &Process[Tdata]{
			Init:            traceInit,
			PutData:         p.Process.PutData,
			GetData:         p.Process.GetData,
			GetProcessState: p.Process.GetProcessState,
			Update:          traceUpdate,
			UpdateWith:      p.Process.UpdateWith,
			Subscribe:       traceSubscribe,
		},
	}
}

func (p *Processor[Tdata]) StartWithDispatch(syncDispatch func(d Dispatch) Dispatch, arg ProcessArg) {
	var (
		dispatch    func(note any)
		processNote func()
	)
	rb := NewRingBuffer()
	data, cmd := p.Process.Init(arg)
	sub := p.Process.Subscribe(data)
	reentered := false
	state := data
	activeSubs := []Subedent{}
	dispatch = func(note any) {
		rb.Push(note)
		if !reentered {
			reentered = true
			processNote()
			reentered = false
		}
	}
	dispatch_ := syncDispatch(dispatch)
	processNote = func() {
		nextNote := rb.Pop()
		for {
			if nextNote == nil {
				break
			}
			note := nextNote
			data, cmd := p.Process.Update(note, state)
			sub := p.Process.Subscribe(data)
			p.Process.PutData(data)
			execCmd(dispatch_, cmd)
			state = data
			activeSubs = changeSubs(dispatch_, diffSubs(activeSubs, sub))
			nextNote = rb.Pop()
		}
	}
	reentered = true
	p.Process.PutData(data)
	execCmd(dispatch_, cmd)
	activeSubs = changeSubs(dispatch_, diffSubs(activeSubs, sub))
	processNote()
	reentered = false
}

func (p *Processor[Tdata]) Start(arg ProcessArg) {
	id := func(d Dispatch) Dispatch {
		return d
	}
	p.StartWithDispatch(id, arg)
}

// func (p *Processor[Tdata]) Start(arg ProcessArg) {
// 	var (
// 		dispatch    func(note any)
// 		processNote func()
// 	)
// 	rb := NewRingBuffer()
// 	data, cmd := p.Process.Init(arg)
// 	sub := p.Process.Subscribe(data)
// 	reentered := false
// 	state := data
// 	activeSubs := []Subedent{}
// 	dispatch = func(note any) {
// 		rb.Push(note)
// 		if !reentered {
// 			reentered = true
// 			processNote()
// 			reentered = false
// 		}
// 	}
// 	processNote = func() {
// 		nextNote := rb.Pop()
// 		for {
// 			if nextNote == nil {
// 				break
// 			}
// 			note := nextNote
// 			data, cmd := p.Process.Update(note, state)
// 			sub := p.Process.Subscribe(data)
// 			p.Process.PutData(data)
// 			execCmd(dispatch, cmd)
// 			state = data
// 			activeSubs = changeSubs(dispatch, diffSubs(activeSubs, sub))
// 			nextNote = rb.Pop()
// 		}
// 	}
// 	reentered = true
// 	p.Process.PutData(data)
// 	execCmd(dispatch, cmd)
// 	activeSubs = changeSubs(dispatch, diffSubs(activeSubs, sub))
// 	processNote()
// 	reentered = false
// }
