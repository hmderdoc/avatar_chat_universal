package bridgecore

type AttachmentKind string

const (
	AttachmentAvatar AttachmentKind = "avatar"
	AttachmentImage  AttachmentKind = "image"
	AttachmentANSI   AttachmentKind = "ansi"
	AttachmentAudio  AttachmentKind = "audio"
	AttachmentLink   AttachmentKind = "link"
	AttachmentRaw    AttachmentKind = "raw"
)

type Attachment struct {
	Kind        AttachmentKind
	Name        string
	ContentType string
	Data        []byte
	URL         string
	Alt         string
}

// Message is the platform-neutral shape bridge adapters should exchange.
// It intentionally does not mirror any one chat API; adapters can map this
// into Discord embeds, Telegram media messages, Matrix m.* events, Slack
// blocks/files, or IRC text.
type Message struct {
	SenderName string
	SenderHost string
	Text       string
	TimeMs     int64
	Origin     Origin

	Attachments []Attachment
}
