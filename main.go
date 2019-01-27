package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Time ...
type Time = time.Time

const ( // map size
	minxbound = 0
	minybound = 0
	maxxbound = 1600
	maxybound = 1200
)

// how many pointers to byte slices can be held at once in Client.writech
// we probably don't need 100 (do some tests to find out?)
const channelBufSize = 100

const maxNumPlayers = 256 // do not set higher than 256
// each client ID is a byte, so we can only have 256 clients at most.
// 256 clients ought to be enough for anyone.
// Setting the max value to 256 does not negatively impact performance.
var availableClientIDs = make(chan byte, maxNumPlayers)

const gamefps = 60 // server game simulation tick rate, also update rate for now.
const tickduration = time.Duration(1000000000 / gamefps)

const maxMsgSize = 9 //how many bytes for maximum message size for each client

const ( // Go doesn't even have enum classes. Muh simplicity.
	msgTypePlayerJoined = 1
	msgTypePlayerLeft   = 2
	msgTypePlayerList   = 3
	msgTypeStateUpdate  = 4
)

var upgrader = websocket.Upgrader{} // use default options

var allowedDirectories = map[string]bool{}
var clients = SynchronizedClientsArray{&sync.Mutex{}, [maxNumPlayers]*Client{}}
var lastclientsmsg [maxNumPlayers]msgUpdate

/* lastclientmsg format:

client id : [msg type, msg content]
e.g
lastclientmsg[id] = [1 byte for msg id, float, float]

*/

type msgUpdate struct {
	msgType byte
	x       float32
	y       float32
}

// SynchronizedClientsArray allows functions to lock the array whilst iterating over it
type SynchronizedClientsArray struct {
	lock *sync.Mutex
	carr [maxNumPlayers]*Client
}

// PlayerInput stores the player's current input state
/* Direction map looks like this:
	1 2 3
	4 5 6
	7 8 9
e.g 1 means "move up and left" and 5 means "stand still".
*/
type PlayerInput struct {
	key int32 //use int32 because there is no atomic.StoreInt8
	// also, key needs to be signed because we want it to produce negative numbers.
}

// Position stores an object's position, used in PlayerCharacter
type Position struct {
	x float32
	y float32
}

// PlayerCharacter stores information about the character - currently only position.
type PlayerCharacter struct {
	pos *Position
}

// Client struct holds client-specific variables.
type Client struct {
	id        byte
	conn      *websocket.Conn
	input     *PlayerInput
	character *PlayerCharacter
	writech   chan []byte // messages written to this channel are immediately sent to the client
	// using []byte instead of *[]byte is significantly faster (somewhere between 50-100% faster)
	stopch chan bool
	lock   *sync.Mutex
}

// Go doesn't have generics so you have to reinvent the wheel for every permutation of types.
// And if you use interface{} then you lose type safety. Thank god for simplicity.
func min(x float32, y float32) float32 {
	if x < y {
		return x
	}
	return y
}
func max(x float32, y float32) float32 {
	if x > y {
		return x
	}
	return y
}

func exponentialRollingAverage5(avg time.Duration, newSample time.Duration) time.Duration {
	avg -= avg / 5
	avg += newSample / 5
	return avg
}

// RandFloat32 generates a random float32 in the range [min...max)
func RandFloat32(min, max float32) float32 {
	r := min + rand.Float32()*(max-min)
	return r
}

// custom httpHandler so we can do serverside caching.
type httpHandler struct {
	cache map[string][]byte
}

func newHTTPHandler() *httpHandler {
	var h httpHandler
	h.cache = make(map[string][]byte)
	return &h
}

// Have to do this because regular expressions in Go are insanely slow.
func isValidFilename(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' {
			return false
		}
	}
	return true
}

func (h httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	filename := "index.html"
	var body []byte
	var err error
	requested := path.Base(r.URL.Path)
	predir := path.Dir(r.URL.Path)
	if _, ok := allowedDirectories[predir]; !ok {
		body = []byte("Error: invalid directory:" + predir + ".")
	} else {
		if isValidFilename(requested) {
			filename = requested
		}
		//fmt.Println(filename, predir)
		if value, exists := h.cache[filename]; exists {
			body = value
		} else {
			if !strings.HasSuffix(predir, "/") {
				predir += "/"
			}
			filepath := "frontend" + predir + filename
			//fmt.Println(filepath)
			body, err = ioutil.ReadFile(filepath)
			if err == nil {
				h.cache[filename] = body
			} else {
				body = []byte("Error: " + filepath + " not found.")
			}
		}
	}
	w.Write(body)
}

func (c *Client) deserializeAndWritePlayerInput(bytes []byte) {
	if len(bytes) != 1 {
		panic(fmt.Sprintf("received message from player is %v bytes long!", len(bytes)))
	}
	if 0 < bytes[0] && bytes[0] < 10 {
		atomic.StoreInt32(&c.input.key, int32(bytes[0]))
	} else {
		panic(fmt.Sprintf("received unknown value from player %v", bytes[0]))
	}
}

//read player keyboard inputs and update input state
func clientReadPump(c *Client, wg *sync.WaitGroup) {
	defer func() {
		log.Println("exited readpump for client", c.id)
		wg.Done()
	}()
	//log.Println("started readpump for client", c.id)
	for {
		select {
		case <-c.stopch:
			log.Println("signal done recvd: ended readpump for client", c.id)
			return
		default:
			_, message, err := c.conn.ReadMessage()
			if err != nil {
				log.Println("ReadMessage error:", err)
				return
			}
			//log.Printf("recv: %d %d %v", mt, len(message), message)
			c.deserializeAndWritePlayerInput(message)
		}
	}
}

//send game state to player
func clientWritePump(c *Client, wg *sync.WaitGroup) {
	defer func() {
		log.Println("exited writepump for client", c.id)
		wg.Done()
	}()
	//log.Println("started writepump for client", c.id)
	//t0, t1 := time.Now(), time.Now()
	for {
		msg, ok := <-c.writech
		if !ok {
			panic("writerb was closed!")
		}
		//log.Println("client", c.id, "time elapsed since last sent message:", time.Since(t1))
		//t1 = time.Now()
		err := c.conn.WriteMessage(websocket.BinaryMessage, msg)
		if err != nil {
			log.Println("WriteMessage error:", err)
			close(c.stopch)
			return
		}
		//log.Println("client", c.id, "time elapsed since last sent message:", time.Since(t0))
		//t0 = time.Now()
		//fmt.Println("sent to client: ", c.id, "msg", msg)
	}
}

func grabClientID() (byte, error) {
	select {
	case id, ok := <-availableClientIDs: //this is the only way to do a non-blocking read in Go
		if !ok {
			panic("availableClientIDs channel was closed wtf!") //this should NEVER happen
		}
		return id, nil
	default:
		return 0, errors.New("no more client IDs available")
	}
}

func releaseClientID(id byte) {
	select {
	case availableClientIDs <- id:
	default: // this should never happen
		fmt.Printf("ERROR: availableClientIDs: %v\n", availableClientIDs)
		panic("Could not write to availableClientIDs, wtf!!!")
	}
}

// NewClient initializes a new Client struct with given websocket.
func NewClient(ws *websocket.Conn, id byte) *Client {
	if ws == nil {
		panic("ws is nil")
	}
	writech := make(chan []byte, 1000)
	input := &PlayerInput{5}
	character := &PlayerCharacter{&Position{RandFloat32(200, 1000), RandFloat32(200, 1000)}}

	return &Client{id, ws, input, character, writech, make(chan bool), &sync.Mutex{}}
}

func wshandler(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil { // can't upgrade connection to websocket for some reason
		log.Print("Failed to upgrade to websocket connection:", err)
		return
	}

	// try to obtain a player ID
	cid, err2 := grabClientID()
	if err2 != nil { // server is full
		err = c.WriteMessage(websocket.BinaryMessage, []byte{5})
		if err != nil {
			log.Println("Failed to tell client that server is full.") //doesn't really matter.
		}
		c.Close() // could use defer here but this is more performant
		log.Println("Server full, connection rejected")
		return
	}

	/*------ Player joins ------*/
	client := NewClient(c, cid)
	//begin critical section
	clients.lock.Lock()

	// could do this in either order, but since new player has to render more sprites it might be fairer
	// to tell new player about current players first
	client.writech <- newPlayerListMsg(client) // we never call Dispose so we can ignore 2nd error code
	newplayerjoinedmsg := make([]byte, 1, 10)
	newplayerjoinedmsg[0] = msgTypePlayerJoined
	appendplayerstate(&newplayerjoinedmsg, cid, client.character.pos.x, client.character.pos.y)
	broadcast(newplayerjoinedmsg) //then tell current players about new player
	clients.carr[cid] = client    //add new player to clients list
	lastclientsmsg[cid].msgType = msgTypePlayerJoined

	clients.lock.Unlock()
	//end critical section
	var wg sync.WaitGroup
	wg.Add(2)
	go clientWritePump(client, &wg)
	go clientReadPump(client, &wg)
	wg.Wait() //TODO: time players out after some period of inactivity.

	/*------ Player leaves ------*/
	fmt.Printf("Client %d left\n", cid)
	c.Close()
	//begin critical section
	clients.lock.Lock()

	clients.carr[cid] = nil                   //remove left player from current players list
	broadcast([]byte{msgTypePlayerLeft, cid}) //tell current players that player has left
	lastclientsmsg[cid].msgType = msgTypePlayerLeft

	clients.lock.Unlock()
	//end critical section
	releaseClientID(cid)
	fmt.Printf("Resources for client %d released\n", cid)
}

func constrainClientToWithinWorldBounds(client *Client) {
	client.character.pos.x = max(client.character.pos.x, minxbound)
	client.character.pos.y = max(client.character.pos.y, minybound)
	client.character.pos.x = min(client.character.pos.x, maxxbound)
	client.character.pos.y = min(client.character.pos.y, maxybound)
}

func updatePlayersPositions() int { //must be called from advancegametick ONLY
	counter := 0
	for c := range clients.carr {
		if clients.carr[c] != nil {
			counter++

			client := clients.carr[c]

			key := atomic.LoadInt32(&client.input.key)
			dx, dy := (key-1)%3-1, (key-1)/3-1 //key needs to be signed for this to work
			scale := int32(8)

			client.character.pos.x += float32(dx * scale)
			client.character.pos.y += float32(dy * scale)

			constrainClientToWithinWorldBounds(client)
		}
	}
	return counter
}

// we have to send positions here because of the don't-resend-positions optimization
func newPlayerListMsg(client *Client) []byte {
	s := []byte{msgTypePlayerList}
	appendplayerstate(&s, client.id, client.character.pos.x, client.character.pos.y)
	for i := range clients.carr {
		if clients.carr[i] != nil {
			cx := clients.carr[i].character.pos.x
			cy := clients.carr[i].character.pos.y
			appendplayerstate(&s, byte(i), cx, cy)
		}
	}
	return s
}

// Big-Endian append
func appendUint32ToByteArray(a *[]byte, n uint32) {
	*a = append(*a, byte(n>>24))
	*a = append(*a, byte(n>>16))
	*a = append(*a, byte(n>>8))
	*a = append(*a, byte(n))
}

func appendplayerstate(b *[]byte, c byte, cx float32, cy float32) {
	*b = append(*b, byte(c))
	appendUint32ToByteArray(b, math.Float32bits(cx))
	appendUint32ToByteArray(b, math.Float32bits(cy))
}

func generateStateMessage(count int) []byte { //must be called from advancegametick ONLY
	b := make([]byte, 1, 1+9*count) //reserve max possible message size to avoid resizing.
	// from my testing, go does not grow the slice until we reach the limit of the slice's capacity.
	b[0] = msgTypeStateUpdate
	for c := range clients.carr {
		if clients.carr[c] != nil {
			client := clients.carr[c]
			cx := client.character.pos.x
			cy := client.character.pos.y
			lastmsg := lastclientsmsg[c]
			// networking optimization: if we already sent this information before, don't send it again
			if lastmsg.msgType == msgTypeStateUpdate && lastmsg.x == cx && lastmsg.y == cy {
				continue
			} else {
				if lastclientsmsg[c].msgType == msgTypePlayerLeft {
					panic("broadcasting to player who has already left") //this should never happen
				}
				lastclientsmsg[c].msgType = msgTypeStateUpdate
				lastclientsmsg[c].x = cx
				lastclientsmsg[c].y = cy

				appendplayerstate(&b, byte(c), cx, cy)
			}
		}
	}
	return b
}

func broadcast(message []byte) {
	for c := range clients.carr {
		if clients.carr[c] != nil {
			clients.carr[c].writech <- message // we never call Dispose so we can ignore 2nd error code
			//writeToPlayerChannel(client, message)
			//fmt.Println("send to a client: ", c, "msg", message)
		}
	}
}

// statistics
var ds = []time.Duration{0, 0, 0, 0, 0}
var ms = []time.Duration{0, 0, 0, 0, 0}
var maxTickDuration time.Duration

func maxDur(d1 time.Duration, d2 time.Duration) time.Duration {
	if d1 > d2 {
		return d1
	}
	return d2
}

func updateStatistics(dur time.Duration, nums ...time.Duration) {
	maxTickDuration = maxDur(maxTickDuration, dur)
	for i, num := range nums {
		ds[i] = exponentialRollingAverage5(ds[i], num)
		ms[i] = maxDur(ms[i], num)
	}
}

var lastTick time.Time
var tickLimit = time.Duration(19 * time.Millisecond)
var oneMs = time.Duration(1 * time.Millisecond)

func advancegametick() {

	dur := time.Since(lastTick)
	fmt.Println("time elapsed since last tick: ", dur)
	/*if dur > tickLimit {
		fmt.Println("time elapsed since last tick: ", dur)
		panic("ticker took too long")
	}*/
	lastTick = time.Now()
	//t0 := time.Now()
	clients.lock.Lock()
	//t1 := time.Now()
	//d1 := time.Since(t0)
	numclients := updatePlayersPositions()
	//t2 := time.Now()
	//d2 := time.Since(t1)
	msg := generateStateMessage(numclients)
	//t3 := time.Now()
	//d3 := time.Since(t2)
	broadcast(msg)
	//t4 := time.Now()
	//d4 := time.Since(t3)
	clients.lock.Unlock()
	//d5 := time.Since(t4)
	if time.Since(lastTick) > oneMs {
		fmt.Println("tick duration: ", time.Since(lastTick))
		panic("tick compute took too long")
	}

	//	fmt.Printf("d1 %v d2 %v d3 %v d4 %v d5 %v\n", d1, d2, d3, d4, d5)
	//updateStatistics(dur, d1, d2, d3, d4, d5)
	//	fmt.Printf("avgs: d1 %v d2 %v d3 %v d4 %v d5 %v\n", ds[0], ds[1], ds[2], ds[3], ds[4])
	//	fmt.Printf("maxs: d1 %v d2 %v d3 %v d4 %v d5 %v\n", ms[0], ms[1], ms[2], ms[3], ms[4])
	maxTickDuration = maxDur(maxTickDuration, dur)
	fmt.Println("max tick duration: ", maxTickDuration)

	//runtime.GC() // very dangerous! don't do this!
}

func mainloop() {
	ticker := time.NewTicker(tickduration)
	defer ticker.Stop()
	for range ticker.C {
		advancegametick()
	}
}

func main() {
	if maxNumPlayers > 256 {
		panic("maxNumPlayers cannot be set to be higher than 256")
	}

	port := os.Getenv("PORT")
	if port == "" {
		log.Fatal("$PORT must be set!!")
	}
	allowedDirectories["/"] = true
	allowedDirectories["/assets"] = true

	debug.SetGCPercent(-1)
	// populate availableClientIDs with free IDs
	for i := 0; i < maxNumPlayers; i++ {
		availableClientIDs <- byte(i)
	}

	lastTick = time.Now()
	go mainloop()
	roothandler := newHTTPHandler()
	http.Handle("/", roothandler)
	http.HandleFunc("/ws", wshandler)

	log.Fatal(http.ListenAndServe(":"+port, nil))
}
