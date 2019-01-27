
# basic game

Basic multiplayer 2D shooter game using websockets. 

This is a bare-bones games that only supports player movement. It is designed to be easily understood. 

Can be deployed directly on Heroku. 

See explanation.md for an explanation of how the game works. 

All of the serverside code is in one file (`main.go`). 

To be done in next version:

1. Breaking up `main.go` into separate packages
2. Clientside interpolation using ping + tick numbers + server timestamps
3. Show players' orientation
4. Shooting bullets that can collide with players
5. Killing / dying / health
6. Sending a different subset of game state to each player

To be done in next next version:

1. Add obstacles
2. Add raycasting so players can't see behind obstacles
