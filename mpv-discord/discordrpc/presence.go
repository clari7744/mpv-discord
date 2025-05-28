package discordrpc

type Activity struct {
	Name           string
	State          string
	Details        string
	Type           int64
	LargeImageKey  string
	LargeImageText string
	SmallImageKey  string
	SmallImageText string

	Party      *ActivityParty
	Secrets    *ActivitySecrets
	Timestamps *ActivityTimestamps
}

type ActivityParty struct {
	ID         string
	Players    int
	MaxPlayers int
}

type ActivitySecrets struct {
	Match    string
	Join     string
	Spectate string
}

type ActivityTimestamps struct {
	Start int64
	End   int64
}

type Presence struct{ *Client }

func NewPresence(cid string) *Presence {
	return &Presence{NewClient(cid)}
}

func (p *Presence) Update(activity Activity) error {
	payload := newActivityPayload()
	payload.Args.Activity = mapActivityMainPayload(activity)
	return p.send(1, payload)
}
