package mock

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"wisp/daemon/internal/api"
)

// Data banks. Generated chats are deterministic for a seed, relative to the start time.

const (
	selfJID  = "61400000000@s.whatsapp.net"
	selfName = "Me"
	bigJID   = "61400009999@s.whatsapp.net"
	bigName  = "Big Chat"
	bigCount = 10000
)

type person struct {
	name string // address-book name ("" = not in address book)
	push string // push name
}

// The first numUserChats people have a 1:1 chat; the rest only appear in groups.
var people = []person{
	{"Alice Nguyen", "Ali"}, {"Bob Smith", "Bob"}, {"Mum", "Linda"}, {"Dad", "Peter"},
	{"Charlotte Wright", "Charlie"}, {"Daniel Kim", "Dan"}, {"Emma Wilson", "Emma W"},
	{"Farhan Ahmed", "Farhan"}, {"Grace Lee", "Gracie"}, {"Hugo Martin", "Hugo"},
	{"Isla Brown", "Isla"}, {"Jack O'Connor", "Jacko"}, {"Kavya Patel", "Kavya"},
	{"Liam Murphy", "Liam"}, {"Mia Chen", "Mia 🌸"}, {"Noah Williams", "Noah"},
	{"Olivia Taylor", "Liv"}, {"Priya Sharma", "Priya"}, {"Quentin Dubois", "Quentin"},
	{"Ruby Jones", "Ruby"}, {"Sam Rossi", "Sam"}, {"Tara Singh", "Tara"},
	{"Umar Hassan", "Umar"}, {"Valentina García", "Vale"}, {"Will Thompson", "Will"},
	{"Xin Yi Zhang", "Xin Yi"}, {"Yusuf Demir", "Yusuf"}, {"Zoe Anderson", "Zo"},
	{"Grandma 💐", "Margaret"}, {"Dr Ellis (GP)", "Harbourside Medical"},
	{"Ben the Plumber", "Ben's Plumbing"}, {"Kenji Tanaka", "Kenji"},
	{"Sophie Müller", "Sophie"}, {"Lucas Silva", "Lucas"}, {"Hannah Cohen", "Hannah"},
	{"Ethan Walker", "Ethan"}, {"Chloe Martin", "Chloe"},
	// group-only contacts
	{"Oscar Reid", "Oscar"}, {"Leah Fischer", "Leah"}, {"Matteo Bianchi", "Matteo"},
	{"Aisha Bello", "Aisha"}, {"Ryan Park", "Ryan"}, {"", "Nina P"}, {"Tom Hughes", "Tom"},
	{"Sarah O'Brien", "Sarah"}, {"", "Diego 🏄"}, {"Freya Larsen", "Freya"},
	{"Josh Campbell", "Josh"}, {"Maya Gupta", "Maya"}, {"", "Ivan K"}, {"Holly Evans", "Holly"},
	{"Arjun Rao", "Arjun"}, {"Ella Robinson", "Ella"}, {"Kai Nakamura", "Kai"},
	{"Lily Turner", "Lily"}, {"Max Becker", "Max"}, {"Rosa Morales", "Rosa"},
	{"Finn Gallagher", "Finn"}, {"Jade Wong", "Jade"}, {"Omar Haddad", "Omar"},
}

const numUserChats = 37

func personJID(p int) string { return fmt.Sprintf("614%08d@s.whatsapp.net", p+1) }

func personName(p int) string {
	if people[p].name != "" {
		return people[p].name
	}
	return people[p].push
}

type groupDef struct {
	name     string
	members  []int // people indices; nil = nMembers random people
	nMembers int
	owner    bool // we created it
}

var groupDefs = []groupDef{
	{name: "Climbing crew 🧗", nMembers: 9, owner: true},
	{name: "Family 👨‍👩‍👧", members: []int{2, 3, 28, 4, 15}},
	{name: "Uni mates", nMembers: 14},
	{name: "Book club 📚", nMembers: 7},
	{name: "Platform team", nMembers: 12},
	{name: "Housemates 🏠", members: []int{11, 19, 33}, owner: true},
	{name: "Sydney Runners", nMembers: 30},
	{name: "Japan trip 2026 🇯🇵", nMembers: 5},
	{name: "Board game night 🎲", nMembers: 6},
	{name: "Sunday soccer ⚽", nMembers: 16},
	{name: "Neighbourhood – Elm St", nMembers: 22},
	{name: "Wisp Announcements 📣", nMembers: 25},
}

func groupJID(g int) string { return fmt.Sprintf("1203630000000000%02d@g.us", g+1) }

var shortTexts = []string{
	"Hey! How's it going?", "On my way 🚗", "Running 10 min late, sorry!", "Sounds good 👍",
	"Did you see the game last night?", "Can you grab milk on the way home?", "Haha yes exactly",
	"I'll call you after work", "What time works for you?", "Thanks so much!", "No worries at all",
	"Lunch tomorrow?", "Just landed ✈️", "Where are we meeting?", "Happy birthday!! 🎉🎂",
	"That's hilarious 😂", "Ok cool", "I'm outside", "Let's do Thursday instead", "Love this",
	"Can't make it tonight unfortunately", "How was the trip?", "Good luck today! You've got this 💪",
	"Did you end up booking it?", "lol", "yep", "Nah all good", "brb", "Same here",
	"Is it going to rain on Saturday?", "Send me the address?", "I left my keys at yours 🤦",
	"Coffee? ☕", "Just finished, heading home now", "Miss you guys", "Totally forgot, sorry!!",
	"See you at the climbing gym at 6", "Who's bringing snacks?", "Fair enough", "Agreed",
	"Wait what", "Ha, classic", "I'm so tired today", "Have a great weekend!", "Morning ☀️",
	"Night night 🌙", "Can we push to 7:30?", "Booked! 🙌", "Is the parking free there?",
	"Remind me tomorrow pls", "Just saw this, sorry for the late reply",
}

var emojiTexts = []string{"😂", "👍", "❤️", "😂😂😂", "🔥🔥", "🙏", "😍", "🎉🎉🎉", "👀", "😅", "🥲", "💯", "🤣👌"}

var markupTexts = []string{
	"This is *really* important, please read before Friday",
	"I'm _so_ ready for the weekend",
	"~Tuesday~ Wednesday works better actually",
	"Run ```make run-mock``` and it should just work",
	"*Reminder:* rent is due on the 1st 🏠",
	"Meeting moved to *3pm*, _not_ 2pm. ~Room 4~ Room 6 now.",
	"The command is ```git rebase -i HEAD~3``` but be careful",
	"_sigh_ the train is delayed *again*",
	"*Bold* _italic_ ~strike~ ```mono``` all in one",
	"Final answer: *yes* ✅",
}

var urlTexts = []string{
	"Check this out https://www.abc.net.au/news/science",
	"Here's the place: https://maps.app.goo.gl/x7Yk2pQ9",
	"https://github.com/tulir/whatsmeow",
	"Tickets are on sale now → https://www.ticketek.com.au/",
	"Recipe I mentioned: https://www.recipetineats.com/lasagna/ so good",
	"Have you read this? https://en.wikipedia.org/wiki/Wayland_(protocol)",
	"Booking link https://www.booking.com/hotel/jp/shibuya-stream.html and the backup https://www.airbnb.com.au/rooms/12345",
	"www.bom.gov.au says 30% chance of showers",
}

var multiLineTexts = []string{
	"Shopping list:\n- milk\n- eggs\n- sourdough\n- coffee beans ☕",
	"Plan for Saturday:\n9am parkrun\n11am brunch at Bills\n2pm beach if it's sunny",
	"Ok so\n\nI just got the call\n\nI GOT THE JOB 🎉🎉",
	"Address:\n42 Wallaby Way\nSydney NSW 2000",
	"1. Pack\n2. Charge the camera\n3. Passports!!\n4. Leave key with Jack",
	"roses are red\nviolets are blue\nthe build is green\nfinally 🙌",
}

var longTexts = []string{
	"So I finally finished the book last night and honestly I'm still thinking about it. The ending completely changed how I saw the first half — all those little details that seemed random were actually setting it up. I don't want to spoil anything but when you get to the chapter with the lighthouse, pay attention to what the narrator *doesn't* say. Let me know when you've finished and we can grab a coffee and talk about it properly, I have so many thoughts.",
	"Quick update on the house stuff: the plumber came this morning and it turns out the leak wasn't the pipe under the sink at all, it was the seal on the dishwasher. He fixed it for $120 which is way less than we feared. He also said the hot water system is getting old and we should think about replacing it in the next year or two. I've put the invoice in the shared folder. Can everyone transfer their share by Friday?",
	"Hey all, a few notes from today's planning session. We agreed to freeze features on the 14th and spend the last week on bug fixes and performance. The memory regression in the sync service is the top priority — it's using about three times what it did last release. Docs owners: please have your sections reviewed by Wednesday. If anything is blocked, shout in the channel rather than waiting for standup. Thanks everyone, great work this sprint! 🚀",
	"Honestly the trip was amazing. Kyoto was the highlight — we got up at 5am to see Fushimi Inari before the crowds and it was completely worth it. The food everywhere was incredible, even the convenience store egg sandwiches (everyone was right about those). Tokyo was a bit overwhelming at first but by day three we'd figured out the trains. Already planning to go back in autumn for the leaves 🍁",
}

var replyBank = []string{
	"Haha 😂", "Sounds good!", "👍", "Ok see you then", "Wait really?", "Can't talk now, call you later",
	"Love it ❤️", "Yep", "Nice one!", "omw", "Let me check and get back to you", "*Definitely* not 😅",
	"lol", "Sure thing", "Good idea", "Hmm let me think about it", "Yesss 🙌", "Oh no 😬",
	"Did you see https://www.abc.net.au/news ?", "Same!!",
}

var captions = []string{"", "", "", "Look at this!", "Sunset from the balcony 🌅", "New climbing shoes 😍", "View from the top",
	"Guess where I am", "Dinner tonight", "The dog stole my seat again 🐶", "Whiteboard from today's meeting", "😂", ""}

var docNames = []struct {
	name, mime string
	size       int64
}{
	{"Lease agreement 2026.pdf", "application/pdf", 482113},
	{"Invoice-4471.pdf", "application/pdf", 88214},
	{"Itinerary_Tokyo_Kyoto.pdf", "application/pdf", 1203311},
	{"Q4 roadmap.pdf", "application/pdf", 2311877},
	{"Grandma's pavlova recipe.pdf", "application/pdf", 54120},
	{"Boarding pass QF25.pdf", "application/pdf", 131072},
	{"climbing-waiver.pdf", "application/pdf", 240533},
	{"Thesis_draft_v7.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", 3388211},
	{"budget-2026.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", 45811},
	{"photos-from-the-weekend.zip", "application/zip", 48877312},
	{"notes.txt", "text/plain", 2213},
}

var reactionEmojis = []string{"👍", "❤️", "😂", "😮", "😢", "🙏", "🔥", "🎉"}

const (
	maxStep = 240 // max seconds between messages inside one session
	day     = 86400
)

type chatSpec struct {
	idx      int
	jid      string
	name     string
	kind     string
	group    int   // groupDefs index, -1 for 1:1
	members  []int // people indices (groups)
	n        int   // generated message count
	unread   int
	last     int64 // timestamp of the newest generated message
	sess     int   // messages per session
	gap      int64 // seconds between sessions
	avatar   bool
	pinned   int64 // pin time, 0 = not pinned
	archived bool
	muted    bool
	readOnly bool
	seed     uint64
}

// mix is the splitmix64 finaliser.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

func rng(a, b uint64) *rand.Rand { return rand.New(rand.NewPCG(mix(a), mix(b^0x9e3779b97f4a7c15))) }

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

// ts is the timestamp of message i: strictly increasing, ts(n-1) == last.
func (cs *chatSpec) ts(i int) int64 {
	back := cs.n - 1 - i
	s, p := back/cs.sess, back%cs.sess
	r := rng(cs.seed^0x5e55, uint64(s))
	var off int64
	if s > 0 {
		off = r.Int64N(cs.gap - int64(cs.sess)*maxStep)
	}
	step := 20 + r.Int64N(maxStep-20)
	return cs.last - int64(s)*cs.gap - off - int64(p)*step
}

// Generated ids encode their position, so lookup by id needs no scan.
func (cs *chatSpec) msgID(i int) string {
	return fmt.Sprintf("3EB0%02X%06X%08X", cs.idx, i, uint32(mix(cs.seed^uint64(i))))
}

func (cs *chatSpec) index(id string) (int, bool) {
	if len(id) != 20 || id[:4] != "3EB0" {
		return 0, false
	}
	i, err := strconv.ParseUint(id[6:12], 16, 32)
	if err != nil || int(i) >= cs.n || cs.msgID(int(i)) != id {
		return 0, false
	}
	return int(i), true
}

func (cs *chatSpec) isGroup() bool { return cs.kind == api.ChatGroup }

// base generates message i without reply or reactions.
func (cs *chatSpec) base(i int) api.Message {
	r := rng(cs.seed, uint64(i))
	m := api.Message{ID: cs.msgID(i), Chat: cs.jid, Timestamp: cs.ts(i), Type: api.MsgText}

	fromMe := r.IntN(100) < 40
	if cs.isGroup() {
		fromMe = r.IntN(100) < 18
	}
	if i >= cs.n-cs.unread {
		fromMe = false
	}
	switch {
	case fromMe:
		m.FromMe, m.Sender, m.SenderName = true, selfJID, selfName
		m.Status = api.StatusRead
		if i == cs.n-1 {
			m.Status = api.StatusDelivered
		}
	case cs.isGroup():
		p := pick(r, cs.members)
		m.Sender, m.SenderName = personJID(p), personName(p)
	default:
		m.Sender, m.SenderName = cs.jid, cs.name
	}

	if cs.isGroup() && i == 0 {
		m.Type, m.FromMe, m.Status = api.MsgSystem, false, ""
		if groupDefs[cs.group].owner {
			m.Sender, m.SenderName = selfJID, selfName
			m.Text = fmt.Sprintf("You created group \"%s\"", cs.name)
		} else {
			p := cs.members[0]
			m.Sender, m.SenderName = personJID(p), personName(p)
			m.Text = fmt.Sprintf("%s created group \"%s\"", personName(p), cs.name)
		}
		return m
	}

	roll := r.IntN(1000)
	switch {
	case cs.isGroup() && roll < 15:
		a, b := pick(r, cs.members), pick(r, cs.members)
		m.Type, m.FromMe, m.Status = api.MsgSystem, false, ""
		m.Sender, m.SenderName = personJID(a), personName(a)
		m.Text = fmt.Sprintf(pick(r, []string{"%[1]s added %[2]s", "%[1]s left", "%[1]s changed this group's icon"}), personName(a), personName(b))
	case roll < 30:
		m.Type, m.Deleted = api.MsgRevoked, true
	case roll < 110:
		m.Type = api.MsgImage
		m.Text = pick(r, captions)
		d := pick(r, [][2]int{{1280, 853}, {720, 1280}, {960, 1280}, {1280, 960}})
		w, h := d[0], d[1]
		m.Media = &api.MediaInfo{Mime: "image/jpeg", Width: w, Height: h, Size: int64(w*h/7 + r.IntN(20000)), HasThumb: true}
	case roll < 150:
		m.Type = api.MsgVoice
		d := 2 + r.IntN(70)
		m.Media = &api.MediaInfo{Mime: "audio/wav", Duration: d, Size: int64(d * 8044)}
	case roll < 170:
		d := pick(r, docNames)
		m.Type = api.MsgDocument
		if r.IntN(3) == 0 {
			m.Text = "Here you go"
		}
		m.Media = &api.MediaInfo{Mime: d.mime, Size: d.size, FileName: d.name}
	default:
		c := r.IntN(100)
		switch {
		case c < 62:
			m.Text = pick(r, shortTexts)
		case c < 70:
			m.Text = pick(r, emojiTexts)
		case c < 79:
			m.Text = pick(r, markupTexts)
		case c < 87:
			m.Text = pick(r, urlTexts)
		case c < 93:
			m.Text = pick(r, multiLineTexts)
		default:
			m.Text = pick(r, longTexts)
		}
		if cs.isGroup() && c%17 == 3 {
			p := pick(r, cs.members)
			mention(&m, personJID(p))
		}
		if cs.isGroup() && c%17 == 5 && !m.FromMe {
			mention(&m, selfJID)
		}
		m.Edited = r.IntN(100) < 4
	}
	if m.Type != api.MsgSystem && m.Type != api.MsgRevoked {
		m.Forwarded = r.IntN(100) < 3
	}
	return m
}

// mention starts m's text with a mention of jid, written as WhatsApp does.
func mention(m *api.Message, jid string) {
	user, _, _ := strings.Cut(jid, "@")
	m.Text = "@" + user + " " + m.Text
	m.Mentions = append(m.Mentions, api.Mention{JID: jid, Name: nameOf(jid), Me: jid == selfJID})
}

// nameOf names someone the way the daemon resolves names.
func nameOf(jid string) string {
	if jid == selfJID {
		return "You"
	}
	for p := range people {
		if personJID(p) == jid {
			return personName(p)
		}
	}
	user, _, _ := strings.Cut(jid, "@")
	return "+" + user
}

// gen generates message i with its quote and reactions.
func (cs *chatSpec) gen(i int) api.Message {
	m := cs.base(i)
	m.Reactions = []api.Reaction{}
	if m.Mentions == nil {
		m.Mentions = []api.Mention{}
	}
	if m.Type == api.MsgSystem || m.Type == api.MsgRevoked {
		return m
	}
	r := rng(cs.seed^0xfeed, uint64(i))
	if i > 2 && r.IntN(100) < 9 {
		q := cs.base(i - 1 - r.IntN(min(i, 12)))
		if q.Type != api.MsgSystem && q.Type != api.MsgRevoked {
			m.ReplyTo = quoteOf(&q)
		}
	}
	if r.IntN(100) < 8 {
		e := pick(r, reactionEmojis)
		count, mine := 1, !m.FromMe
		if cs.isGroup() {
			count = 1 + r.IntN(min(len(cs.members), 6))
			mine = r.IntN(3) == 0
		}
		m.Reactions = append(m.Reactions, api.Reaction{Emoji: e, Count: count, Mine: mine})
		if e2 := pick(r, reactionEmojis); cs.isGroup() && e2 != e && r.IntN(3) == 0 {
			m.Reactions = append(m.Reactions, api.Reaction{Emoji: e2, Count: 1 + r.IntN(3)})
		}
	}
	return m
}

func quoteOf(m *api.Message) *api.Quote {
	return &api.Quote{ID: m.ID, Sender: m.Sender, SenderName: m.SenderName, FromMe: m.FromMe, Type: m.Type, Preview: api.PreviewText(m)}
}

// buildSpecs creates the chats; anchor is "now" in unix seconds.
func buildSpecs(seed uint64, anchor int64) []*chatSpec {
	var specs []*chatSpec
	add := func(cs *chatSpec) {
		cs.idx = len(specs)
		cs.seed = mix(seed ^ uint64(cs.idx+1)*0x2545f4914f6cdd1d)
		specs = append(specs, cs)
	}
	r := rng(seed, 0xc4a7)

	add(&chatSpec{jid: bigJID, name: bigName, kind: api.ChatUser, group: -1, n: bigCount,
		unread: 3, last: anchor - 4*60, sess: 6, avatar: true})
	for p := 0; p < numUserChats; p++ {
		ago := int64(r.Float64()*r.Float64()*r.Float64()*120*day) + int64(r.IntN(3600))
		cs := &chatSpec{jid: personJID(p), name: personName(p), kind: api.ChatUser, group: -1,
			n: 12 + r.IntN(150), last: anchor - ago, sess: 3 + r.IntN(6), avatar: p%6 != 4,
			archived: p == 29 || p == 30}
		if p == 2 {
			cs.pinned = anchor - 3*day
		}
		if ago < 3*day && r.IntN(10) < 4 {
			cs.unread = 1 + r.IntN(4)
		}
		add(cs)
	}
	for g, gd := range groupDefs {
		members := gd.members
		if members == nil {
			members = r.Perm(len(people))[:gd.nMembers]
		}
		ago := int64(r.Float64()*r.Float64()*60*day) + int64(r.IntN(3600))
		cs := &chatSpec{jid: groupJID(g), name: gd.name, kind: api.ChatGroup, group: g, members: members,
			n: 40 + r.IntN(280), last: anchor - ago, sess: 4 + r.IntN(8), avatar: g != 8 && g != 10,
			muted: g == 6 || g == 10, readOnly: g == 11}
		if g == 1 {
			cs.pinned = anchor - 2*day
		}
		if ago < 7*day && r.IntN(2) == 0 {
			cs.unread = 1 + r.IntN(12)
		}
		add(cs)
	}
	for _, cs := range specs {
		sessions := (cs.n + cs.sess - 1) / cs.sess
		span := int64(120 * day)
		if cs.n > 1000 {
			span = 400 * day
		}
		cs.gap = max(span/int64(sessions), int64(2*cs.sess*maxStep+1))
	}
	return specs
}
