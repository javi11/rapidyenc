package rapidyenc

// State is the current Decoder State, the values refer to the previously seen
// characters in the stream, which influence how some sequences need to be handled.
//
// The shorthands represent:
// CR (\r), LF (\n), EQ (=), DT (.)
type State int

const (
	StateCRLF     State = 0 // default
	StateEQ       State = 1
	StateCR       State = 2
	StateNone     State = 3
	StateCRLFDT   State = 4
	StateCRLFDTCR State = 5
	StateCRLFEQ   State = 6 // may actually be "\r\n.=" in raw decoder
)

// End is the state for incremental decoding, whether the end of the yEnc data was reached.
type End int

const (
	EndNone    End = 0 // end not reached
	EndControl End = 1 // \r\n=y sequence found, src points to byte after 'y'
	EndArticle End = 2 // \r\n.\r\n sequence found, src points to byte after last '\n'
)
