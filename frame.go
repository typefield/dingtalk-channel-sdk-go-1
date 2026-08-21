package channel

// Stream 线协议数据结构（见 SPEC §2）。

type subscriptionType string

const (
	subCallback subscriptionType = "CALLBACK"
	subSystem   subscriptionType = "SYSTEM"
)

type subscription struct {
	Type  string `json:"type"`
	Topic string `json:"topic"`
}

type openConnectionRequest struct {
	ClientID      string         `json:"clientId"`
	ClientSecret  string         `json:"clientSecret"`
	Subscriptions []subscription `json:"subscriptions"`
	UA            string         `json:"ua"`
	LocalIP       string         `json:"localIp,omitempty"`
}

type openConnectionResponse struct {
	Endpoint string `json:"endpoint"`
	Ticket   string `json:"ticket"`
}

// frame 是服务端下发的数据帧。
type frame struct {
	SpecVersion string            `json:"specVersion"`
	Type        string            `json:"type"`
	Time        int64             `json:"time"`
	Headers     map[string]string `json:"headers"`
	Data        string            `json:"data"`
}

func (f *frame) topic() string     { return f.Headers["topic"] }
func (f *frame) messageID() string { return f.Headers["messageId"] }

// frameAck 是回给服务端的 ACK（必须回，否则重投）。
type frameAck struct {
	Code    int               `json:"code"`
	Headers map[string]string `json:"headers"`
	Message string            `json:"message"`
	Data    string            `json:"data"`
}

func successAck(messageID, data string) frameAck {
	if data == "" {
		data = `{"success":true}`
	}
	return frameAck{
		Code:    200,
		Headers: map[string]string{"contentType": "application/json", "messageId": messageID},
		Message: "ok",
		Data:    data,
	}
}
