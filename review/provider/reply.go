package provider

import "github.com/sglavoie/seneca/review/usage"

// Reply is what a provider answered: the reply's text, the model that wrote it,
// and what the request consumed. Usage is nil when no response came back.
type Reply struct {
	Text  []byte
	Model string
	Usage *usage.Tokens
}
