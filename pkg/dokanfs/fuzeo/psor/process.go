package psor // import "perkeep.org/pkg/dokanfs/fuzeo/psor"

import (
	"container/ring"
	"fmt"
)

func toConsole(format string, a ...any) (n int, err error) {
	return fmt.Printf(format, a)
}

type ProcessArg interface {
	IsProcessArg()
}

type ProcessState = uint8

const (
	processStatePending = iota
	processStateSettled
)

type Dispatch = func(note any)

type Effect = func(dispatch Dispatch)

type Cmd = []Effect

var CmdNone = []Effect{}

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

type ringBuffer struct {
	h *ring.Ring
	r *ring.Ring
}

func newRingBuffer() *ringBuffer {
	r := ring.New(1)
	return &ringBuffer{
		h: r,
		r: r,
	}
}

func (x *ringBuffer) push(v any) {
	x.r.Value = v
	s := ring.New(1)
	x.r.Link(s)
	x.r = s
}

func (x *ringBuffer) pop() any {
	if x.h != x.r {
		rl := x.r.Unlink(1)
		// debugf("ring buffer length %d", rl.Len())
		x.h = x.r.Next()
		return rl.Value
	}
	return nil
}

func (x *ringBuffer) peek() any {
	if x.h != x.r {
		v := x.h.Value
		return v
	}
	return nil
}

func (p *Processor[Tdata]) Start(arg ProcessArg) {
	var (
		dispatch    func(note any)
		processNote func()
	)
	rb := newRingBuffer()
	data, cmd := p.Process.Init(arg)
	sub := p.Process.Subscribe(data)
	reentered := false
	state := data
	activeSubs := []Subedent{}
	dispatch = func(note any) {
		rb.push(note)
		if !reentered {
			reentered = true
			processNote()
			reentered = false
		}
	}
	processNote = func() {
		nextNote := rb.pop()
		for {
			if nextNote == nil {
				break
			}
			note := nextNote
			data, cmd := p.Process.Update(note, state)
			sub := p.Process.Subscribe(data)
			p.Process.PutData(data)
			execCmd(dispatch, cmd)
			state = data
			activeSubs = changeSubs(dispatch, diffSubs(activeSubs, sub))
			nextNote = rb.pop()
		}
	}
	reentered = true
	p.Process.PutData(data)
	execCmd(dispatch, cmd)
	activeSubs = changeSubs(dispatch, diffSubs(activeSubs, sub))
	processNote()
	reentered = false
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

// // / Trace all the updates to the console
// // let withConsoleTrace (program: Program<'arg, 'model, 'msg, 'view>) =
// func WithConsoleLog(p Process) {
// 	// let traceInit (arg:'arg) =
// 	traceInit := func(arg) {
// 		// let initModel,cmd = program.init arg
// 		initModel, cmd := p.init(arg)
// 		// Log.toConsole ("Initial state:", initModel)
// 		toConsole("Initial state: %v", initModel)
// 		// initModel,cmd
// 		return initModel, cmd
// 	}

// 	// let traceUpdate msg model =
// 	traceUpdate := func(msg, model) {
// 		// Log.toConsole ("New message:", msg)
// 		toConsole("New message: %v", msg)
// 		// let newModel,cmd = program.update msg model
// 		newModel, cmd := program.update(msg, model)
// 		// Log.toConsole ("Updated state:", newModel)
// 		toConsole("Updated state: %v", newModel)
// 		// newModel,cmd
// 		return newModel, cmd
// 	}

// 	// let traceSubscribe model =
// 	traceSubscribe := func(model) {
// 		// let sub = program.subscribe model
// 		sub := process.subscribe(model)
// 		// Log.toConsole ("Updated subs:", sub |> List.map fst)
// 		toConsole("Updated subs: %v", sub)
// 		// sub
// 		return sub
// 	}

// 	return &Process{
// 		init:            traceInit,
// 		putData:         p.putData,
// 		getData:         p.getData,
// 		getProcessState: p.getProcessState,
// 		update:          traceUpdate,
// 		updateWith:      p.updateWith,
// 		subscribe:       traceSubscribe,
// 	}
// }

func (p *Processor[Tdata]) StartWithDispatch(syncDispatch func(d Dispatch) Dispatch, arg ProcessArg) {
	var (
		dispatch    func(note any)
		processNote func()
	)
	rb := newRingBuffer()
	data, cmd := p.Process.Init(arg)
	sub := p.Process.Subscribe(data)
	reentered := false
	state := data
	activeSubs := []Subedent{}
	dispatch = func(note any) {
		rb.push(note)
		if !reentered {
			reentered = true
			processNote()
			reentered = false
		}
	}
	dispatch_ := syncDispatch(dispatch)
	processNote = func() {
		nextNote := rb.pop()
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
			nextNote = rb.pop()
		}
	}
	reentered = true
	p.Process.PutData(data)
	execCmd(dispatch_, cmd)
	activeSubs = changeSubs(dispatch_, diffSubs(activeSubs, sub))
	processNote()
	reentered = false
}
