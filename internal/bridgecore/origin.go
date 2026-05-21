package bridgecore

import (
	"fmt"
	"strings"
)

// Origin identifies the external system a bridged message came from. Bridge
// adapters should compare full origins, not just protocol names, so messages
// from IRC/libera can still be forwarded to IRC/efnet.
type Origin struct {
	Protocol string
	Network  string
	Channel  string
}

func NewOrigin(protocol, network, channel string) Origin {
	return Origin{
		Protocol: cleanPart(protocol),
		Network:  cleanPart(network),
		Channel:  cleanPart(channel),
	}
}

func (o Origin) Empty() bool {
	return o.Protocol == "" && o.Network == "" && o.Channel == ""
}

func (o Origin) Key() string {
	if o.Empty() {
		return ""
	}
	return o.Protocol + ":" + o.Network + "/" + o.Channel
}

func (o Origin) Matches(other Origin) bool {
	return strings.EqualFold(o.Protocol, other.Protocol) &&
		strings.EqualFold(o.Network, other.Network) &&
		strings.EqualFold(o.Channel, other.Channel)
}

func (o Origin) Display() string {
	switch {
	case o.Network != "" && o.Channel != "":
		return o.Network + "/" + o.Channel
	case o.Network != "":
		return o.Network
	default:
		return o.Protocol
	}
}

func FormatHostMarker(o Origin) string {
	if o.Empty() {
		return ""
	}
	return "BRIDGE:" + o.Key()
}

func ParseHostMarker(host string) (Origin, bool) {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "BRIDGE:") {
		return parseKey(strings.TrimPrefix(host, "BRIDGE:"))
	}
	// Backward compatibility with the first IRC bridge release.
	if strings.HasPrefix(host, "IRC:") {
		origin, ok := parseLegacyIRC(strings.TrimPrefix(host, "IRC:"))
		if ok {
			return origin, true
		}
	}
	return Origin{}, false
}

func parseKey(key string) (Origin, bool) {
	colon := strings.IndexByte(key, ':')
	slash := strings.LastIndexByte(key, '/')
	if colon <= 0 || slash <= colon+1 || slash == len(key)-1 {
		return Origin{}, false
	}
	return NewOrigin(key[:colon], key[colon+1:slash], key[slash+1:]), true
}

func parseLegacyIRC(value string) (Origin, bool) {
	slash := strings.LastIndexByte(value, '/')
	if slash <= 0 || slash == len(value)-1 {
		return Origin{}, false
	}
	return NewOrigin("irc", value[:slash], value[slash+1:]), true
}

func cleanPart(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func MustParseHostMarker(host string) Origin {
	o, ok := ParseHostMarker(host)
	if !ok {
		panic(fmt.Sprintf("bridgecore: invalid host marker %q", host))
	}
	return o
}
