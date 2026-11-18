package research

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

// scriptTimeout bounds one command. A research source that hangs must not
// freeze the run that asked for it.
const scriptTimeout = 30 * time.Second

// scriptOutputLimit bounds what a command may return.
const scriptOutputLimit = 128 << 10

// callScript runs a command the user configured.
//
// The command runs directly, without a shell, and the request's text is
// substituted into a single argument — so a query cannot become a second
// command. Creating such a source is the user's own decision: the assistant is
// not allowed to register one.
func (c *Client) callScript(ctx context.Context, connection *domainresearch.Connection, request appresearch.CallRequest) (appresearch.CallResult, error) {
	args, err := splitCommand(connection.Endpoint)
	if err != nil {
		return appresearch.CallResult{}, err
	}
	if len(args) == 0 {
		return appresearch.CallResult{}, appresearch.ErrInvalidCall
	}
	input := strings.TrimSpace(request.Query)
	if input == "" {
		input = strings.TrimSpace(request.Body)
	}
	if input == "" {
		input = strings.TrimSpace(request.Operation)
	}
	substituted := false
	for i := range args {
		for _, placeholder := range []string{"{input}", "{query}"} {
			if strings.Contains(args[i], placeholder) {
				args[i] = strings.ReplaceAll(args[i], placeholder, input)
				substituted = true
			}
		}
	}
	if !substituted && input != "" {
		args = append(args, input)
	}

	commandCtx, cancel := context.WithTimeout(ctx, scriptTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, args[0], args[1:]...)
	// Both streams are bounded while they are produced. Buffering first and
	// trimming afterwards would let a runaway command fill memory with
	// gigabytes before the limit is applied; here the command itself is
	// stopped once it goes over.
	stdout := newBoundedBuffer(scriptOutputLimit, cancel)
	stderr := newBoundedBuffer(scriptOutputLimit, cancel)
	command.Stdout = stdout
	command.Stderr = stderr
	started := time.Now()
	runErr := command.Run()
	duration := time.Since(started)
	output := stdout.String()
	if stdout.Overflowed() {
		output += "\n… (вывод сокращён)"
	}
	if runErr != nil {
		reason := strings.TrimSpace(stderr.String())
		if stdout.Overflowed() {
			reason = fmt.Sprintf("вывод команды превысил допустимый размер (%d КиБ)", scriptOutputLimit>>10)
		}
		if reason == "" {
			reason = runErr.Error()
		}
		return appresearch.CallResult{Status: 0, ContentType: "text/plain", Body: output, Duration: duration},
			fmt.Errorf("команда источника завершилась ошибкой: %s", truncate(reason, 400))
	}
	return appresearch.CallResult{
		Status:      0,
		ContentType: "text/plain",
		Body:        output,
		Duration:    duration,
	}, nil
}

// boundedBuffer collects output up to a limit and stops the command when it
// goes over. Discarding the excess would not be enough: the process would keep
// producing it, so the limit cancels the command as well.
type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
	stop     func()
}

func newBoundedBuffer(limit int, stop func()) *boundedBuffer {
	return &boundedBuffer{limit: limit, stop: stop}
}

func (b *boundedBuffer) Write(payload []byte) (int, error) {
	if b.overflow {
		return len(payload), nil
	}
	room := b.limit - b.buffer.Len()
	if room <= 0 || len(payload) > room {
		if room > 0 {
			b.buffer.Write(payload[:room])
		}
		b.overflow = true
		if b.stop != nil {
			b.stop()
		}
		return len(payload), nil
	}
	return b.buffer.Write(payload)
}

func (b *boundedBuffer) String() string { return b.buffer.String() }

// Overflowed reports whether the command produced more than the limit.
func (b *boundedBuffer) Overflowed() bool { return b.overflow }

// splitCommand splits a command line into arguments, honouring single and
// double quotes and backslash escapes. It is deliberately not a shell: no
// expansion, no redirection, no chaining.
func splitCommand(command string) ([]string, error) {
	args := make([]string, 0, 4)
	var current strings.Builder
	quote := rune(0)
	inWord := false
	escaped := false
	for _, r := range command {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
			inWord = true
		case r == '\\' && quote != '\'':
			escaped = true
			inWord = true
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				args = append(args, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("%w: команда заканчивается обратной косой чертой", appresearch.ErrInvalidCall)
	}
	if quote != 0 {
		return nil, fmt.Errorf("%w: в команде не закрыта кавычка", appresearch.ErrInvalidCall)
	}
	if inWord {
		args = append(args, current.String())
	}
	if len(args) == 0 {
		return nil, appresearch.ErrInvalidCall
	}
	return args, nil
}
