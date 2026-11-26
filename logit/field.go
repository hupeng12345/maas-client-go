package logit

import (
	"crypto/md5"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	json "github.com/bytedance/sonic"
)

// Field represents a single structured key-value pair in a log entry.
// The Value method returns the rendered key and value as strings that can be
// written out directly.
type Field interface {
	Value() (key, value string)
}

// FieldFunc is a lazily evaluated function that builds its Field only when a log
// line is actually emitted. This avoids expensive field construction when the
// level is filtered out, which improves performance.
type FieldFunc func() Field

// ============================================================================
// Generic fields: one internal implementation for the primitive numeric types
// ============================================================================

// valueField is a generic field that provides uniform storage and formatting for
// any type. It replaces the former intField / uintField / floatField structs and
// other near-duplicates.
type valueField[T any] struct {
	key    string
	val    T
	format func(T) string
}

// Value returns the key and the formatted value string.
func (f *valueField[T]) Value() (string, string) {
	return f.key, f.format(f.val)
}

// FieldOf creates a FieldFunc for any type; a custom format function is required.
// It is the base constructor of the generic field system. For the common numeric
// types, prefer the IntOf / UintOf / FloatOf helpers.
func FieldOf[T any](name string, value T, format func(T) string) FieldFunc {
	f := &valueField[T]{key: name, val: value, format: format}
	return func() Field { return f }
}

// IntOf creates a FieldFunc for a signed integer; it covers int / int8 / int16 /
// int32 / int64.
func IntOf[T ~int | ~int8 | ~int16 | ~int32 | ~int64](name string, value T) FieldFunc {
	return FieldOf(name, value, func(v T) string { return strconv.FormatInt(int64(v), 10) })
}

// UintOf creates a FieldFunc for an unsigned integer; it covers uint / uint8 /
// uint16 / uint32 / uint64.
func UintOf[T ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](name string, value T) FieldFunc {
	return FieldOf(name, value, func(v T) string { return strconv.FormatUint(uint64(v), 10) })
}

// FloatOf creates a FieldFunc for a floating-point number; it covers float32 /
// float64.
func FloatOf[T ~float32 | ~float64](name string, value T) FieldFunc {
	return FieldOf(name, value, func(v T) string { return strconv.FormatFloat(float64(v), 'f', -1, 64) })
}

// ============================================================================
// Backward-compatible API: delegates to the generic implementations and keeps
// the original function signatures
// ============================================================================

// IntField creates a FieldFunc for an int (backward compatible; delegates to IntOf).
func IntField(name string, value int) FieldFunc { return IntOf(name, value) }

// Int64Field creates a FieldFunc for an int64 (backward compatible; delegates to
// IntOf).
func Int64Field(name string, value int64) FieldFunc { return IntOf(name, value) }

// UintField creates a FieldFunc for a uint (backward compatible; delegates to UintOf).
func UintField(name string, value uint) FieldFunc { return UintOf(name, value) }

// Uint64Field creates a FieldFunc for a uint64 (backward compatible; delegates to
// UintOf).
func Uint64Field(name string, value uint64) FieldFunc { return UintOf(name, value) }

// Float32Field creates a FieldFunc for a float32 (backward compatible; delegates
// to FloatOf).
func Float32Field(name string, value float32) FieldFunc { return FloatOf(name, value) }

// Float64Field creates a FieldFunc for a float64 (backward compatible; delegates
// to FloatOf).
func Float64Field(name string, value float64) FieldFunc { return FloatOf(name, value) }

// ============================================================================
// String fields
// ============================================================================

// stringField holds a string value together with its newline-escaping configuration.
type stringField struct {
	key          string // field key name
	val          string // pre-rendered field value
	allowNewline bool   // when false, newlines are escaped as \n
}

// stringConfig holds the configuration options for a string field.
type stringConfig struct {
	allowNewline bool // whether to keep newlines in the output
}

// Value returns the key and the rendered string value.
func (sf *stringField) Value() (string, string) {
	return sf.key, sf.val
}

// StrConfigFunc is an option constructor for string fields.
type StrConfigFunc func(*stringConfig)

// WithStrAllowNewline returns an option that controls whether newlines are kept
// or escaped. When false (the default), newlines are escaped as \n so that each
// entry stays on a single line.
func WithStrAllowNewline(allowNewline bool) StrConfigFunc {
	return func(c *stringConfig) {
		c.allowNewline = allowNewline
	}
}

// StringField creates a FieldFunc for a UTF-8 string value. By default, newlines
// are escaped to prevent multi-line log entries.
func StringField(name string, value string, opts ...StrConfigFunc) FieldFunc {
	config := &stringConfig{}
	for _, opt := range opts {
		opt(config)
	}
	if !config.allowNewline {
		value = escapeNewline(value)
	}
	f := &stringField{
		key: name,
		val: value,
	}
	return func() Field {
		return f
	}
}

// ============================================================================
// Boolean field
// ============================================================================

// boolField holds a boolean value.
type boolField struct {
	key string // field key name
	val bool   // boolean value
}

// Value returns the key and either "true" or "false".
func (f *boolField) Value() (string, string) {
	return f.key, strconv.FormatBool(f.val)
}

// BoolField creates a FieldFunc for a boolean value.
func BoolField(name string, value bool) FieldFunc {
	return func() Field {
		return &boolField{
			key: name,
			val: value,
		}
	}
}

// ============================================================================
// Error field
// ============================================================================

// errorField holds an error value.
type errorField struct {
	key string // field key name
	err error  // the error (may be nil)
}

// Value returns the key and the error message, or "<nil>" if the error is nil.
func (f *errorField) Value() (string, string) {
	if f.err == nil {
		return f.key, "<nil>"
	}
	return f.key, f.err.Error()
}

// ErrorField creates a FieldFunc for an error. A nil error renders as "<nil>".
func ErrorField(name string, err error) FieldFunc {
	return func() Field {
		return &errorField{
			key: name,
			err: err,
		}
	}
}

// ============================================================================
// Arbitrary-value field
// ============================================================================

// anyField holds an arbitrary value that is formatted with fmt.Sprintf.
type anyField struct {
	key string // field key name
	val any    // the arbitrary value to format
}

// Value returns the key and the "%v"-formatted string.
func (f *anyField) Value() (string, string) {
	return f.key, fmt.Sprintf("%v", f.val)
}

// AnyField creates a FieldFunc for an arbitrary value, formatted with "%v".
func AnyField(name string, value any) FieldFunc {
	return func() Field {
		return &anyField{
			key: name,
			val: value,
		}
	}
}

// ============================================================================
// Truncating string field (StringAbsField)
// ============================================================================

// stringAbsField holds a string value that has already been abstracted (truncated).
type stringAbsField struct {
	key string // field key name
	val string // the pre-processed and truncated value
}

// Value returns the key and the pre-processed string value.
func (s *stringAbsField) Value() (string, string) {
	return s.key, s.val
}

// stringAbsConfig holds the configuration options for string abstraction.
// StringAbsField (abs = abstract/absolute) truncates long strings to a fixed
// preview length while keeping the beginning, middle, and end, and includes the
// MD5 of the full content so that integrity can be verified.
type stringAbsConfig struct {
	head         int                       // runes to keep from the beginning
	middle       int                       // runes to keep from the middle
	tail         int                       // runes to keep from the end
	format       func(input string) string // custom format function (optional)
	allowNewline bool                      // whether to keep newlines in the output
}

// StringAbsParamFunc is an option constructor for StringAbsField.
type StringAbsParamFunc func(input *stringAbsConfig)

// WithStringAbsHead sets the number of runes to keep from the beginning of the string.
func WithStringAbsHead(head int) StringAbsParamFunc {
	return func(input *stringAbsConfig) {
		input.head = head
	}
}

// WithAllowNewline controls whether newlines are kept (true) or escaped (false).
func WithAllowNewline(allowNewLine bool) StringAbsParamFunc {
	return func(input *stringAbsConfig) {
		input.allowNewline = allowNewLine
	}
}

// WithStringAbsMiddle sets the number of runes to keep from the middle of the string.
func WithStringAbsMiddle(middle int) StringAbsParamFunc {
	return func(input *stringAbsConfig) {
		input.middle = middle
	}
}

// WithStringAbsTail sets the number of runes to keep from the end of the string.
func WithStringAbsTail(tail int) StringAbsParamFunc {
	return func(input *stringAbsConfig) {
		input.tail = tail
	}
}

// WithStringAbsFormatFunc sets a custom truncating format function. When one is
// provided, the default truncation logic is skipped.
func WithStringAbsFormatFunc(format func(input string) string) StringAbsParamFunc {
	return func(input *stringAbsConfig) {
		input.format = format
	}
}

// defaultFormat performs the default string abstraction. It keeps head + middle +
// tail runes with an ellipsis (...) between the segments. The output also includes
// the total rune count of the full string and its MD5 hash.
// The format is: <len=N md5=hash preview=head...middle...tail>
func defaultFormat(conf *stringAbsConfig, input string) string {
	if input == "" {
		return ""
	}

	totalRunes := utf8.RuneCountInString(input)

	headN := min(conf.head, totalRunes)
	midN := min(conf.middle, totalRunes-headN)
	tailN := min(conf.tail, totalRunes)

	if headN+midN+tailN >= totalRunes {
		return input
	}

	tailStartRune := totalRunes - tailN
	tailStartRune = max(headN+midN, tailStartRune)

	headEnd := len(input)
	midEnd := len(input)
	tailStart := len(input)

	var i, runeIdx int

	for i < len(input) {
		if runeIdx == headN && headEnd == len(input) {
			headEnd = i
		}
		if runeIdx == headN+midN && midEnd == len(input) {
			midEnd = i
		}
		if runeIdx == tailStartRune {
			tailStart = i
			break
		}
		_, size := utf8.DecodeRuneInString(input[i:])
		i += size
		runeIdx++
	}

	headEnd = min(headEnd, len(input))
	midEnd = min(midEnd, len(input))
	tailStart = min(tailStart, len(input))

	if midEnd < headEnd {
		midEnd = headEnd
	}

	if tailStart < midEnd {
		tailStart = midEnd
	}

	head := input[:headEnd]
	middle := input[headEnd:midEnd]
	tail := input[tailStart:]

	var b strings.Builder
	b.Grow(len(input)/2 + 64)

	b.WriteString("<len=")
	b.WriteString(strconv.Itoa(totalRunes))

	b.WriteString(" md5=")

	sum := md5.Sum([]byte(input))

	const hexTable = "0123456789abcdef"
	for _, v := range sum {
		b.WriteByte(hexTable[v>>4])
		b.WriteByte(hexTable[v&0x0f])
	}

	b.WriteString(" preview=")
	b.WriteString(head)
	b.WriteString("...")
	b.WriteString(middle)
	b.WriteString("...")
	b.WriteString(tail)
	b.WriteByte('>')

	return b.String()
}

// StringAbsField creates a FieldFunc that truncates a potentially very long string
// by keeping its beginning, middle, and end, together with the MD5 hash of the
// full content. It is useful for logging large payloads (JSON, HTML) without
// blowing up the log volume.
//
// By default, head keeps 200 runes, middle 200 runes, and tail 200 runes. Use
// WithStringAbsHead, WithStringAbsMiddle, and WithStringAbsTail to customize this.
func StringAbsField(name string, value string, opts ...StringAbsParamFunc) FieldFunc {
	param := &stringAbsConfig{
		head:   200,
		middle: 200,
		tail:   200,
	}
	for _, opt := range opts {
		opt(param)
	}
	if param.format == nil {
		param.format = func(input string) string {
			return defaultFormat(param, value)
		}
	}
	val := param.format(value)
	if !param.allowNewline {
		val = escapeNewline(val)
	}
	return func() Field {
		return &stringAbsField{
			key: name,
			val: val,
		}
	}
}

// escapeNewline replaces newlines and carriage returns with the \n and \r escape
// sequences. This keeps the log output on a single line when newlines are not
// allowed.
func escapeNewline(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// ============================================================================
// Truncating JSON fields (JSONAbsField / JSONDetailAbsField)
// ============================================================================

// jsonAbsField holds a JSON-serializable value with configurable log truncation.
type jsonAbsField struct {
	key  string
	val  any
	conf *jsonAbsConfig
}

// jsonDetailAbsField is a high-performance JSON field that truncates strings while
// streaming. Unlike jsonAbsField, it encodes the JSON directly with no
// intermediate copy.
type jsonDetailAbsField struct {
	key  string
	val  any
	conf *jsonAbsConfig
}

// jsonAbsConfig controls head/middle/tail truncation when formatting JSON logs.
type jsonAbsConfig struct {
	head         int                    // runes to keep from the beginning
	middle       int                    // runes to keep from the middle
	tail         int                    // runes to keep from the end
	format       func(input any) string // custom format function (optional)
	allowNewline bool                   // whether to keep newlines in the output
}

// JSONAbsParamFunc is an option constructor for JSONAbsField.
type JSONAbsParamFunc func(input *jsonAbsConfig)

// WithJSONAbsHead sets the number of runes to keep from the beginning of a string.
func WithJSONAbsHead(head int) JSONAbsParamFunc {
	return func(input *jsonAbsConfig) {
		input.head = head
	}
}

// WithJSONAbsAllowNewline controls whether newlines are kept (true) or escaped (false).
func WithJSONAbsAllowNewline(allowNewLine bool) JSONAbsParamFunc {
	return func(input *jsonAbsConfig) {
		input.allowNewline = allowNewLine
	}
}

// WithJSONAbsMiddle sets the number of runes to keep from the middle of a string.
func WithJSONAbsMiddle(middle int) JSONAbsParamFunc {
	return func(input *jsonAbsConfig) {
		input.middle = middle
	}
}

// WithJSONAbsTail sets the number of runes to keep from the end of a string.
func WithJSONAbsTail(tail int) JSONAbsParamFunc {
	return func(input *jsonAbsConfig) {
		input.tail = tail
	}
}

// WithJSONAbsFormatFunc sets a custom format function. When one is provided, the
// default truncation logic is skipped.
func WithJSONAbsFormatFunc(format func(input any) string) JSONAbsParamFunc {
	return func(input *jsonAbsConfig) {
		input.format = format
	}
}

// defaultJSONAbsFormat serializes v to JSON after truncating long strings
// recursively through processValue. Compact JSON is used by default, unless
// allowNewline requests indented output.
func defaultJSONAbsFormat(conf *jsonAbsConfig, v any) string {
	sac := &stringAbsConfig{
		head:         conf.head,
		middle:       conf.middle,
		tail:         conf.tail,
		format:       nil,
		allowNewline: conf.allowNewline,
	}
	val := processValue(v, sac)
	if conf.allowNewline {
		bs, err := json.MarshalIndent(val, "", "    ")
		if err != nil {
			return ""
		}
		return string(bs)
	}
	bs, err := json.Marshal(val)
	if err != nil {
		return ""
	}
	return string(bs)
}

// processValue walks maps and slices recursively and applies the string truncation
// rules to string and UTF-8 byte values.
func processValue(v any, conf *stringAbsConfig) any {

	switch val := v.(type) {
	case string:
		return defaultFormat(conf, val)
	case []byte:
		if !utf8.Valid(val) {
			return fmt.Sprintf("<bytes len=%d>", len(val))
		}
		return defaultFormat(
			conf,
			string(val),
		)
	case map[string]any:
		for k, vv := range val {
			val[k] = processValue(vv, conf)
		}
		return val
	case []any:
		for i, vv := range val {
			val[i] = processValue(vv, conf)
		}
		return val
	default:
		return v
	}
}

// normalizeJSON round-trips v through JSON serialization and deserialization so that
// nested structures become walkable map[string]any / []any values.
func normalizeJSON(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if err = json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// Value returns the key and the formatted JSON string produced by the configured
// formatter (long strings are truncated by default).
//
// Note that a nil value renders as the JSON "null" rather than "{}". Collapsing a
// nil value into an empty object would lose the information that the field has no
// value, and downstream consumers parsing the JSON could no longer tell nil apart
// from an empty map or an empty struct.
func (f *jsonAbsField) Value() (string, string) {
	return f.key, f.conf.format(f.val)
}

// JSONAbsField creates a FieldFunc for an arbitrary value serialized as JSON. Long
// strings inside maps and slices are truncated with the same head/middle/tail
// rules as StringAbsField. By default each segment keeps 200 runes; use
// WithJSONAbsHead, WithJSONAbsMiddle, and WithJSONAbsTail to override that. Use
// WithJSONAbsFormatFunc to replace the whole formatting step.
func JSONAbsField(name string, value any, opts ...JSONAbsParamFunc) FieldFunc {
	param := &jsonAbsConfig{
		head:   200,
		middle: 200,
		tail:   200,
	}

	for _, opt := range opts {
		opt(param)
	}
	if param.format == nil {
		param.format = func(input any) string {
			return defaultJSONAbsFormat(param, value)
		}
	}

	return func() Field {
		return &jsonAbsField{
			key:  name,
			val:  value,
			conf: param,
		}
	}
}

// ============================================================================
// Duration field
// ============================================================================

type TimeFormatMethod int8

const (
	// TimeFormatMS emits a bare millisecond number (such as 1500). It is compact and
	// easy for machines to parse, but carries no unit. Note that a reader cannot
	// tell 1500 from 1500ns or 1500µs, so prefer TimeFormatMSUnit unless the field
	// name already states the unit.
	TimeFormatMS TimeFormatMethod = 0
	// TimeFormatAdaptiveStr emits a string with an adaptive unit, such as "0.5ms" /
	// "1.5s" / "1m30.5s" / "1h0m0s".
	TimeFormatAdaptiveStr TimeFormatMethod = 1
	// TimeFormatMSUnit emits a millisecond value with the ms unit, such as "0.5ms" or
	// "1500ms". It uses the same millisecond scale as TimeFormatMS but keeps the unit,
	// so readers cannot misjudge the magnitude.
	TimeFormatMSUnit TimeFormatMethod = 2
)

// timeDurationField records a time.Duration with a compact pure-ASCII rendering
// (milliseconds below 1s, then s/m/h tiers).
type timeDurationField struct {
	key    string
	val    time.Duration
	format TimeFormatMethod
}

type TimeDurationFunc func(f *timeDurationField)

func WithTimeDurationFormatMethod(fm TimeFormatMethod) TimeDurationFunc {
	return func(f *timeDurationField) {
		f.format = fm
	}
}

// TimeDurationField creates a FieldFunc for a duration; see formatDurationFieldValue
// for the output format.
func TimeDurationField(name string, value time.Duration, opts ...TimeDurationFunc) FieldFunc {
	return func() Field {
		f := &timeDurationField{
			key:    name,
			val:    value,
			format: TimeFormatMS,
		}
		for _, opt := range opts {
			opt(f)
		}
		return f
	}
}

// Value returns the key and the formatted duration string.
func (f *timeDurationField) Value() (string, string) {
	switch f.format {
	case TimeFormatMS:
		return f.key, strconv.FormatInt(int64(f.val/time.Millisecond), 10)
	case TimeFormatMSUnit:
		return f.key, formatDurationMSUnit(f.val)
	case TimeFormatAdaptiveStr:
		return f.key, formatDurationFieldValue(f.val)
	default:
		return f.key, formatDurationFieldValue(f.val)
	}
}

// formatDurationMSUnit renders a duration in milliseconds and keeps the unit suffix,
// such as "0.5ms" or "1500ms".
func formatDurationMSUnit(d time.Duration) string {
	if d == 0 {
		return "0ms"
	}
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', -1, 64) + "ms"
}

// formatDurationFieldValue renders a duration for logging: pure ASCII, with the
// smallest unit being ms below 1s, then s, and then XmYs or XhYmZs for quick
// scanning (the µs and ns units are never used).
func formatDurationFieldValue(d time.Duration) string {
	if d < 0 {
		return "-" + formatDurationFieldValue(-d)
	}
	if d < time.Second {
		if d == 0 {
			return "0ms"
		}
		return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', -1, 64) + "ms"
	}
	if d < time.Minute {
		return strconv.FormatFloat(float64(d)/float64(time.Second), 'f', -1, 64) + "s"
	}
	if d < time.Hour {
		m := int(d / time.Minute)
		s := float64(d%time.Minute) / float64(time.Second)
		return strconv.Itoa(m) + "m" + strconv.FormatFloat(s, 'f', -1, 64) + "s"
	}
	h := int(d / time.Hour)
	rem := d % time.Hour
	m := int(rem / time.Minute)
	s := float64(rem%time.Minute) / float64(time.Second)
	return strconv.Itoa(h) + "h" + strconv.Itoa(m) + "m" + strconv.FormatFloat(s, 'f', -1, 64) + "s"
}

// ============================================================================
// High-performance streaming JSON field (JSONDetailAbsField)
// ============================================================================

// JSONDetailAbsField is a high-performance JSON field that truncates strings while
// streaming. It encodes the JSON directly without intermediate normalization or
// copying, truncating only string and []byte values. The JSON structure itself is
// kept intact. By default each segment keeps 200 runes; use WithJSONAbsHead,
// WithJSONAbsMiddle, and WithJSONAbsTail to override that.
func JSONDetailAbsField(name string, value any, opts ...JSONAbsParamFunc) FieldFunc {
	param := &jsonAbsConfig{
		head:   200,
		middle: 200,
		tail:   200,
	}

	for _, opt := range opts {
		opt(param)
	}

	return func() Field {
		return &jsonDetailAbsField{
			key:  name,
			val:  value,
			conf: param,
		}
	}
}

// Value returns the key and the truncated JSON string produced by streaming
// encoding. It is fast: the JSON is built in a single pass while long strings are
// truncated.
func (f *jsonDetailAbsField) Value() (string, string) {
	buf := &strings.Builder{}
	buf.Grow(1024)

	obj := normalizeJSON(f.val)
	if obj == nil {
		return f.key, "{}"
	}

	writeJSONValue(buf, obj, f.conf)
	result := buf.String()

	if !f.conf.allowNewline {
		return f.key, result
	}
	return f.key, escapeNewline(result)
}

// writeJSONValue writes v to buf recursively while truncating string and []byte
// values. This is the fast path: no intermediate copy, a single encoding pass.
func writeJSONValue(buf *strings.Builder, v any, conf *jsonAbsConfig) {
	if v == nil {
		buf.WriteString("null")
		return
	}

	switch val := v.(type) {
	case string:
		writeJSONString(buf, val, conf)
	case []byte:
		if !utf8.Valid(val) {
			buf.WriteByte('"')
			buf.WriteString(fmt.Sprintf("<bytes len=%d>", len(val)))
			buf.WriteByte('"')
			return
		}
		writeJSONString(buf, string(val), conf)
	case map[string]any:
		buf.WriteByte('{')
		first := true
		for k, vv := range val {
			if !first {
				buf.WriteByte(',')
			}
			first = false
			writeJSONString(buf, k, conf)
			buf.WriteByte(':')
			writeJSONValue(buf, vv, conf)
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, vv := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSONValue(buf, vv, conf)
		}
		buf.WriteByte(']')
	default:
		b, _ := json.Marshal(val)
		buf.Write(b)
	}
}

// writeJSONString writes a truncated JSON string with the appropriate escaping.
func writeJSONString(buf *strings.Builder, s string, conf *jsonAbsConfig) {
	sac := &stringAbsConfig{
		head:         conf.head,
		middle:       conf.middle,
		tail:         conf.tail,
		allowNewline: conf.allowNewline,
	}
	truncated := defaultFormat(sac, s)

	buf.WriteByte('"')
	start := 0
	for i := 0; i < len(truncated); {
		c := truncated[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			i++
			continue
		}
		if start < i {
			buf.WriteString(truncated[start:i])
		}
		switch c {
		case '"', '\\':
			buf.WriteByte('\\')
			buf.WriteByte(c)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			buf.WriteByte(c)
		}
		i++
		start = i
	}
	if start < len(truncated) {
		buf.WriteString(truncated[start:])
	}
	buf.WriteByte('"')
}
