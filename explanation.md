# Explanation

Client side game.js is pretty self explanatory so I will mainly explain how the server works. 

Every time a new player joins he is served static content which then tries to access `/ws` on the server. 

The handler for that address (`wshandler`) upgrades the connection into a websocket. If there is room for a new player, the new player is assigned an ID and 2 goroutines are spun up: a read pump (`clientReadPump` )and a write pump (`clientWritePump`). Also, whenever a player joins, his position is broadcasted to all existing players and he also receives the positions of all the existing players. In this way everyone's game states are kept in sync so we can use the "don't resend positions for players that haven't moved" optimization. 

The read pump reads keys sent from the player and directly writes these into the player's input key variable (which is an atomic). This ensures that player inputs are updated as soon as possible. 

The write pump takes messages from the player's write channel (`writech`) and sends these to the player. Thus, all communication in the program is done by writing messages to each player's write channel (see `broadcast`). This channel is given a large buffer (1000) to avoid blocking. 

The main game loop happens in `mainloop`. This function creates a ticker that signals once every game tick interval (default is 16.666ms) and calls `advancegametick` which is where the main logic of the game happens. 

`advancegametick` updates client's positions based on their keyboard input and then generates a state message describing each client's position and sends it to all the clients. If a client has not moved since the last tick then his position is not included in the message. 

Keep in mind that websockets is TCP so frequent packet loss (e.g wifi) == unplayable lag. 