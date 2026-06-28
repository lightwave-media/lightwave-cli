package cli

// Test-only exports: expose unexported helpers to the external cli_test
// package without widening the public API (standard Go export_test.go idiom).

var (
	DeriveTenet   = deriveTenet
	ReverseDomain = reverseDomain
	TitleFirst    = titleFirst
	ParsePsOutput = parsePsOutput
	TopByCPU      = topByCPU
)
