// Package protocol converts request bodies, response bodies and SSE streams
// between the OpenAI Chat Completions, OpenAI Responses and Anthropic Messages
// protocols through a shared bridge model.
//
// System One and Embeddings are recognized but never translated: Valid accepts
// them, Path maps them to their endpoint and WriteError gives them the OpenAI
// error envelope. Neither has a bridge encoding, so a cross-protocol
// conversion fails, and the gateway forwards their payloads verbatim.
package protocol

// Protocol names the wire protocol of a client-facing endpoint or of an
// upstream model.
type Protocol string

// The protocol constants name the wire protocol of a client endpoint or an
// upstream model, and each is both: the gateway serves a request in the
// client's protocol and speaks the route's protocol upstream, converting
// between the two when they differ. Chat is OpenAI Chat Completions, Responses
// is OpenAI Responses, Anthropic is Anthropic Messages, and SystemOne and
// Embeddings are described below.
const (
	Chat      Protocol = "chat"
	Responses Protocol = "responses"
	Anthropic Protocol = "anthropic"
	// SystemOne is OpenCode Zen's structured decision endpoint. A request pairs
	// a free-form state with typed questions and the reply carries typed
	// answers, so the payload shares no shape with the message protocols the
	// bridge converts and is forwarded as-is.
	SystemOne Protocol = "systemone"
	// Embeddings is the OpenAI Embeddings endpoint. Its request and response
	// are relayed verbatim on /v1/embeddings; a message protocol has no
	// embedding equivalent, so it is never converted.
	Embeddings Protocol = "embeddings"
)

// Valid reports whether p is one of the protocols defined above.
func Valid(p Protocol) bool {
	return p == Chat || p == Responses || p == Anthropic || p == SystemOne || p == Embeddings
}

// Kind groups a protocol into the family the console labels a model with: the
// message protocols (Chat, Responses, Anthropic) are "chat", SystemOne is
// "decision" and Embeddings is "embeddings". Any other value is returned
// unchanged, so a protocol added later shows up under its own name instead of
// silently joining another family.
func Kind(p Protocol) string {
	switch p {
	case Chat, Responses, Anthropic:
		return "chat"
	case SystemOne:
		return "decision"
	case Embeddings:
		return "embeddings"
	}
	return string(p)
}

// Path returns the endpoint path that serves a protocol, relative to an API
// base. The gateway's own routes and the upstream endpoints share these
// paths. An unknown protocol gets the Chat path.
func Path(protocol Protocol) string {
	switch protocol {
	case Responses:
		return "/v1/responses"
	case Anthropic:
		return "/v1/messages"
	case SystemOne:
		return "/v1/systemone"
	case Embeddings:
		return "/v1/embeddings"
	default:
		return "/v1/chat/completions"
	}
}
