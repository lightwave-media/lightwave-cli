package secrets

// Test-only doors to unexported pieces; testpackage skips *export_test.go.
var (
	CallerFromARN = callerFromARN
	ProbeEnv      = probeEnv
	AWSError      = awsError
	NewSSM        = newSSM
	NewSTS        = newSTS
)

func MetaReaderFor(api describeAPI) MetaReader { return ssmMeta{api: api} }

func WriterFor(api putAPI, c Caller) ValueWriter { return rotatorWriter{api: api, caller: c} }
