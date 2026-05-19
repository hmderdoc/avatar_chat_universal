package ircbridge

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

type IRCMessage struct {
	Prefix  string
	Nick    string
	Command string
	Params  []string
}

type IRCClient struct {
	cfg IRCConfig

	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
	mu   sync.Mutex
}

func NewIRCClient(cfg IRCConfig) *IRCClient {
	return &IRCClient{cfg: cfg}
}

func (c *IRCClient) Connect(ctx context.Context) error {
	addr := net.JoinHostPort(c.cfg.Host, fmt.Sprintf("%d", c.cfg.Port))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("irc: dial %s: %v", addr, err)
	}
	if c.cfg.TLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: c.cfg.Host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return fmt.Errorf("irc: tls handshake: %v", err)
		}
		conn = tlsConn
	}
	c.conn = conn
	c.r = bufio.NewReaderSize(conn, 64*1024)
	c.w = bufio.NewWriter(conn)
	if c.cfg.Password != "" {
		_ = c.raw("PASS %s", c.cfg.Password)
	}
	if c.cfg.Username == "" {
		c.cfg.Username = c.cfg.Nick
	}
	if c.cfg.Realname == "" {
		c.cfg.Realname = c.cfg.Nick
	}
	_ = c.raw("NICK %s", c.cfg.Nick)
	_ = c.raw("USER %s 0 * :%s", c.cfg.Username, c.cfg.Realname)
	if err := c.waitRegistered(ctx); err != nil {
		_ = conn.Close()
		return err
	}
	if c.cfg.NickServPassword != "" {
		_ = c.Privmsg("NickServ", "IDENTIFY "+c.cfg.NickServPassword)
	}
	return c.Join(c.cfg.Channel)
}

func (c *IRCClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *IRCClient) Join(channel string) error {
	return c.raw("JOIN %s", channel)
}

func (c *IRCClient) Privmsg(target, msg string) error {
	for _, line := range splitIRCLines(msg) {
		if err := c.raw("PRIVMSG %s :%s", target, line); err != nil {
			return err
		}
	}
	return nil
}

func (c *IRCClient) Action(target, msg string) error {
	return c.Privmsg(target, "\x01ACTION "+msg+"\x01")
}

func (c *IRCClient) ReadLoop(ctx context.Context, out chan<- IRCMessage) error {
	defer close(out)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := c.r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return err
			}
			return fmt.Errorf("irc: read: %v", err)
		}
		msg := ParseIRCLine(line)
		if msg.Command == "" {
			continue
		}
		if msg.Command == "PING" && len(msg.Params) > 0 {
			_ = c.raw("PONG :%s", msg.Params[len(msg.Params)-1])
			continue
		}
		out <- msg
	}
}

func (c *IRCClient) waitRegistered(ctx context.Context) error {
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	defer c.conn.SetReadDeadline(time.Time{})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := c.r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("irc: registration: %v", err)
		}
		msg := ParseIRCLine(line)
		switch msg.Command {
		case "PING":
			if len(msg.Params) > 0 {
				_ = c.raw("PONG :%s", msg.Params[len(msg.Params)-1])
			}
		case "001", "376", "422":
			return nil
		case "433":
			return fmt.Errorf("irc: nickname already in use: %s", c.cfg.Nick)
		case "ERROR":
			if len(msg.Params) > 0 {
				return fmt.Errorf("irc: %s", msg.Params[len(msg.Params)-1])
			}
			return fmt.Errorf("irc: server error")
		}
	}
}

func (c *IRCClient) raw(format string, args ...interface{}) error {
	line := fmt.Sprintf(format, args...)
	if len(line) > 510 {
		line = line[:510]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.w == nil {
		return fmt.Errorf("irc: not connected")
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err := c.w.WriteString(line + "\r\n"); err != nil {
		return err
	}
	return c.w.Flush()
}

func ParseIRCLine(line string) IRCMessage {
	line = strings.TrimRight(line, "\r\n")
	var msg IRCMessage
	if strings.HasPrefix(line, ":") {
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			return msg
		}
		msg.Prefix = line[1:sp]
		msg.Nick = msg.Prefix
		if bang := strings.IndexByte(msg.Nick, '!'); bang >= 0 {
			msg.Nick = msg.Nick[:bang]
		}
		line = strings.TrimLeft(line[sp+1:], " ")
	}
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		msg.Command = strings.ToUpper(line)
		return msg
	}
	msg.Command = strings.ToUpper(line[:sp])
	rest := strings.TrimLeft(line[sp+1:], " ")
	for rest != "" {
		if strings.HasPrefix(rest, ":") {
			msg.Params = append(msg.Params, rest[1:])
			break
		}
		sp = strings.IndexByte(rest, ' ')
		if sp < 0 {
			msg.Params = append(msg.Params, rest)
			break
		}
		msg.Params = append(msg.Params, rest[:sp])
		rest = strings.TrimLeft(rest[sp+1:], " ")
	}
	return msg
}

func splitIRCLines(s string) []string {
	s = strings.ReplaceAll(s, "\r", " ")
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		for len(p) > 400 {
			out = append(out, p[:400])
			p = strings.TrimSpace(p[400:])
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
