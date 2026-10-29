# merilo

merilo is for building state machines in Go that your tests verify
exhaustively. Every machine imports `code.eitanity.com/merilo/fsm` for the
parts all machines share, so each one is built the same way and only its
own transitions are left to write. A world is one snapshot of the system
the machines form: every machine and its surroundings, such as messages in
flight. In your tests, merilo checks every world the system can reach and
fuzzes beyond them. `merilovet` keeps your code within the rules those
tests rely on.

`fsm` is the only part of merilo compiled into your program: it imports
nothing, allocates nothing and never panics. merilo needs Go 1.22 or later
and only the standard library.

## Build a machine

This walks through `examples/turnstile` and `examples/order`.

### 1. Import `fsm` and name the states and events

```go
import "code.eitanity.com/merilo/fsm"
```

`fsm` gives every machine the same `fsm.Outcome`: `Moved` took effect, by
changing the machine or returning actions, `Ignored` and `Rejected` left it as
it was, and `Invalid` refuses something impossible, such as a machine never
built with `New`.

Your states and events are enums: a named integer type with its own
constants. The zero value of each is an unexported sentinel, so a value
nobody set deliberately matches no case and is refused.

```go
type State uint8

const (stateNone State = iota; Locked; Unlocked; numStates)

type Event uint8

const (eventNone Event = iota; EvCoin; EvPush; numEvents)
```

### 2. Write the machine and its `Fire`

A machine is a value type with unexported fields. `New` builds it, `State()`
reads it, and `Fire` is the only way it changes: it returns the next machine
and the outcome.

```go
type Machine struct{ state State }

func New() Machine             { return Machine{state: Locked} }
func (m Machine) State() State { return m.state }

func (m Machine) Fire(e Event) (Machine, fsm.Outcome) {
	switch m.state {
	case Locked:
		switch e {
		case EvCoin:
			m.state = Unlocked
			return m, fsm.Moved
		case EvPush:
			return m, fsm.Rejected
		default:
			return m, fsm.Invalid
		}
	case Unlocked:
		// ...
	default:
		return m, fsm.Invalid
	}
}
```

Write every switch on an enum with every constant and a `default` that
returns `fsm.Invalid`, so a new state or event cannot slip through. Nothing
panics. Keep the fields to values: no pointers, slices or maps. The machine
is then comparable, and `==` tells two apart.

### 3. Hold it

The code that owns a machine replaces it with its own `Fire` result, in place.
`out.Err()` is nil for `Moved` and `Ignored` and an `fsm.Refusal` for anything
else, so every holder handles outcomes the same way:

```go
c.m, acts, out = c.m.Fire(e)
if err := out.Err(); err != nil {
	return fmt.Errorf("%v in %v: %w", e, c.m.State(), err)
}
```

Never copy a machine or take its address. A copy written back later would
revert it without a transition.

### 4. Test that every pair is handled

An internal test lists machines and events, with position 0 the zero value
of each. `Total` fires every event at every machine. Position 0 of either
must be refused as `Invalid`. Any other pair fails if it is `Invalid`, or if
it is `Ignored` or `Rejected` but still changed the machine:

```go
type table struct{}

func (table) Machines() int { return int(numStates) }
func (table) Events() int   { return int(numEvents) }

func (table) Fire(m, e int) (fsm.Outcome, bool) {
	before := Machine{state: State(m)}
	next, out := before.Fire(Event(e))
	return out, next == before
}

func (table) Name(m, e int) string { return fmt.Sprintf("state %d / event %d", m, e) }

func TestTotal(t *testing.T) { merilo.Total(t, table{}) }
```

### 5. Explore and fuzz the system

An adapter in a `_test.go` file puts the machines in their environment, such
as the network, the timers and the user. Steps are numbered kinds:

```go
type System interface {
	Reset()                // return to the initial world
	Kinds() int            // number of step kinds
	Enabled(k int) bool    // whether step kind k can be taken now
	Apply(k int) error     // take step kind k; non-nil error is a violation
	World() any            // the current world, a comparable value
	Check() error          // invariants on the current world
	Tuple() string         // every machine's control state, formatted
	Settled() bool         // stopped by design
	Describe(k int) string // one readable trace line
	Invariants() []string  // names of the checks Check performs
}
```

`Apply` drives the machines through `Fire` exactly as production code does.
`World` usually returns the adapter itself, `return *s`: its machines, and its
model of the environment in fixed arrays.

Machines that are completely separate, such as an app's business logic and
its user authentication, are separate systems: give each its own adapter,
and its own `Explore`, `Fuzz` and `Total`, in its own package's tests.

```go
var opts = merilo.Options{MaxStates: 1000, RequireComplete: true}

func TestExplore(t *testing.T) { merilo.Explore(t, newSys(), opts) }

func FuzzTurnstile(f *testing.F) {
	merilo.Fuzz(f, newSys(), merilo.FuzzOptions{MaxDepth: 10, Explore: opts})
}
```

`Explore` is a breadth-first search, so the first violation it finds has
the shortest trace. From each world it replays the path from `Reset`,
takes every enabled kind and checks the invariants. A replay that reaches a
different world means something outside the path, such as the clock, leaked
in. It reports:

- invariant violations;
- deadlocks: no step is enabled and `Settled` is false;
- apply errors and undeclared mode changes;
- nondeterminism.

`MaxStates`, the most worlds it stores, is its one bound and has no
default. It stops at the first world that doesn't fit, and every run logs
what it proved: `complete=true` means every reachable world was checked,
and otherwise every world within `depth` steps of the start was.

```text
merilo: complete=true states=38 depth=6 invariants=1 [no one passes without paying]
```

`Fuzz` runs that search first. For each combination of machine states the
search reached, it takes the shortest list of steps that gets there, and
starts fuzzing from it. So fuzzing goes further than the search. `MaxDepth`
is the most steps one fuzz input takes.

### 6. Keep each counterexample

A violation fails the test with a readable trace:

```text
#05 deliver n0->n1 Submit   | n0:Pending n1:Pending
#06 drop    n1->n0 Ack      | n0:Pending n1:Pending
#07 tick    n0              | n0:Failed  n1:Pending
VIOLATION invariant "no failure while peer pending" at step 7
```

Copy its kinds into a test that calls `merilo.Replay`, which fails while the
bug is there and when the trace no longer fits the system.

### 7. Run merilovet

```sh
go run code.eitanity.com/merilo/cmd/merilovet
```

It checks your packages and their tests against the rules above:
- every switch is complete, including those on `fsm.Outcome`;
- fields are written only through `Fire`;
- machines are never copied or pointed to;
- every outcome is read;
- counters are 64-bit and bounded;
- every `World` returns a value `==` can compare.

It finds machines from their types, with no markers and no suppressions.
It reads the files one target builds, so run it once for each
`GOOS`/`GOARCH` you ship, and name your build tags for code behind a
constraint such as `//go:build tinygo`:

```sh
GOOS=linux GOARCH=arm go run code.eitanity.com/merilo/cmd/merilovet -tags tinygo,baremetal
```

`merilovet -h` prints its help and exit codes. `merilovet -version` prints
the git tag it was built from, or a pseudo-version naming the commit when
built between tags.

## Patterns

- **Side effects are actions.** `Fire` returns what the holder must do as a
  third result, `Fire(E) (Machine, Actions, fsm.Outcome)`, and the holder
  does it. `Fire` itself does no I/O, so replay can check it. `Actions` is a
  bit set of the machine's own type, so one machine's actions cannot be
  handed to another's holder, and the holder performs them in declaration
  order:

  ```go
  type Actions uint64

  const (ActSend Actions = 1 << iota; ActResend; ActStartTimer; ActCancelTimer)

  for a := order.Actions(1); a != 0; a <<= 1 {
  	if acts&a != 0 {
  		c.do(a)
  	}
  }
  ```

- **Counters are `fsm.Counter`.** A counter has a limit set when it is built,
  and `Inc` refuses at it instead of wrapping:

  ```go
  case EvTimeout:
  	retries, ok := m.retries.Inc()
  	if !ok {
  		m.state = Failed
  		return m, 0, fsm.Moved
  	}
  	m.retries = retries
  	return m, ActResend | ActStartTimer, fsm.Moved
  ```

- **Timers are events.** The machine asks with `ActStartTimer` and
  `ActCancelTimer`. The holder owns the `time.Timer` and fires `EvTimeout`
  when it expires. A stopped timer can still deliver a late expiry. If timers
  only stop as the machine leaves the waiting state, that state ignores it
  (`examples/order`). If a running timer can be replaced, carry a generation
  in the event so `Fire` ignores the stale one (`examples/watchdog`).
- **Any other number in a machine is `int64` or `uint64`,** compared with its
  bound before each change and refused at it, as the watchdog's generation
  is. An `int` wraps at 2³¹ on 32-bit targets.
- **Modes derive from state.** `Mode()` is a switch on the state. `Fire`
  refuses an undeclared change of mode and reports a permitted one as an
  action.
- **Steps with an argument get one kind each.** Delivering the message in
  slot `i` is kind `kDeliver+i`. Keep messages sorted in a fixed array, so the
  order they were sent in doesn't split one world into many
  (`examples/order`).

## Examples

| Example | Shows |
| --- | --- |
| [`examples/turnstile`](examples/turnstile) | The smallest machine: one mode, no actions |
| [`examples/order`](examples/order) | Payment order with retries, modes, a timer and a lossy, duplicating network; [`cmd/orderd`](examples/order/cmd/orderd) is a holder |
| [`examples/mutex`](examples/mutex) | Lock clients against an idempotent server, with a seeded duplicate-grant bug |
| [`examples/twophase`](examples/twophase) | Two-phase commit coordinator with voters, recovery modes and two seeded bugs |
| [`examples/watchdog`](examples/watchdog) | Timeout events that carry a generation, so `Fire` itself drops a stale timeout |
| [`examples/fib`](examples/fib) | Tail recursion: the machine is the call `fib(n, a, b)` and holds all three arguments. Each `Fire` evaluates it once: when `n` is 0 the call returns `a`, and otherwise the machine makes the tail call by becoming `fib(n-1, b, a+b)`, refusing as `TooLarge` a sum that would not fit in 1024 bits. A `big.Int` holds a slice, so `==` could not compare a machine that held one. The machine holds each number as 16 words of 64 bits, adds them with carry, and returns them as `big.Int`. The holder's loop is the trampoline: it fires until the call returns |
| [`examples/bom`](examples/bom) | A bill of materials rolled up by tail recursion: each part's subtotal is its value plus each line's quantity times that part's subtotal, and the system's need of each part is the quantities multiplied down every path. The machine is the call `walk(stack, need, subtotal)`: it holds the bill, the stack of work still to do and the numbers, and each `Fire` visits the top entry, which is the tail call. It refuses a part on the path to itself, numbers that would not fit in 64 bits, and more calls than its limit; the holder in `bom/rollup` only names the parts and loops until the call returns |

The adapters in `examples/` are meant to be copied.

## What merilo promises

- **No files, network, processes, environment or global state.** Fuzz inputs
  stay in Go's own `testdata/fuzz`.
- **`fsm`, the part in your program, imports nothing,** allocates nothing and
  never panics.
- **No panics from merilo.** An error is returned or reported through
  `testing.TB`, and merilovet exits 2 on its own faults. A `World` that `==`
  cannot compare still panics inside Go's map; merilovet refuses one.
- **Determinism.** No randomness or wall-clock time, and no dependence on
  map order.
- **No allocation after setup, no silent overflow, and safe to run
  concurrently.**
- **No recursion through your adapter.** An adapter method that calls
  back into merilo is refused with an error.

## Development

```sh
make ci     # verify build fmt vet lint fsm selfcheck race test-386 fuzz seeded cover vuln
make qemu   # the test suite under qemu-user on 11 more CPUs
```

| Target | Runs |
| --- | --- |
| `verify` | `go mod verify`, a tidy `go.mod` with no `require` lines, no tracked binaries |
| `lint` | golangci-lint, pinned and installed into `bin/` |
| `fsm` | merilovet on this module, once per target in `FSM_TARGETS` |
| `selfcheck` | Code safety and overflow self-check on `merilo`, `fsm` and `cmd/merilovet` |
| `test-386` | vet and tests with 32-bit `int` |
| `qemu` | Tests on arm, arm64, loong64, mips, mipsle, mips64, mips64le, ppc64, ppc64le, riscv64 and s390x; needs qemu-user, and `QEMU_TARGETS` picks a subset |
| `fuzz` | Every fuzz target for `FUZZTIME` (default 30s) |
| `seeded` | Fuzzing seeded by a shallow search must find the seeded bug beyond it |
| `vuln` | govulncheck |

## License

Copyright 2026 Eitanity Systems VCC. Licensed under the [Apache License, Version 2.0](LICENSE).
