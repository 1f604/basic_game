
// front end code is based off this Phaser example here: https://labs.phaser.io/view.html?src=src\games\topdownShooter\topdown_playerFocus.js

var config = {
    type: Phaser.WEBGL,
    width: 1280,
    height: 720,
    physics: {
      default: 'arcade',
      arcade: {
        gravity: { y: 0 },
        debug: false
      }
    },
    scene: {
        preload: preload,
        create: create,
        update: update,
        extend: {
                    player: null,
                    healthpoints: null,
                    reticle: null,
                    moveKeys: null,
                    time: 0,
                }
    }
};

limits = {
    left: -config.width / 2,
    right: config.width * 3/2,
    top: -config.height / 2,
    bottom: config.height * 3/2
}

var gPlayer = {}

var game = new Phaser.Game(config);

function preload ()
{
    // Load in images and sprites
    this.load.spritesheet('player_handgun', 'assets/player_handgun.png',
        { frameWidth: 66, frameHeight: 60 }
    ); // Made by tokkatrain: https://tokkatrain.itch.io/top-down-basic-set
    this.load.image('target', 'assets/crosshair136.png');
    this.load.image('background', 'assets/underwater1.png');
}

function isWebGLSupported()
{
    const contextOptions = { stencil: true, failIfMajorPerformanceCaveat: true };

    try
    {
        if (!window.WebGLRenderingContext)
        {
            return false;
        }

        const canvas = document.createElement('canvas');
        let gl = canvas.getContext('webgl', contextOptions) || canvas.getContext('experimental-webgl', contextOptions);

        const success = !!(gl && gl.getContextAttributes().stencil);

        if (gl)
        {
            const loseContext = gl.getExtension('WEBGL_lose_context');

            if (loseContext)
            {
                loseContext.loseContext();
            }
        }

        gl = null;

        return success;
    }
    catch (e)
    {
        return false;
    }
}

/**
 * Returns a random integer between min (inclusive) and max (inclusive).
 * The value is no lower than min (or the next integer greater than min
 * if min isn't an integer) and no greater than max (or the next integer
 * lower than max if max isn't an integer).
 * Using Math.round() will give you a non-uniform distribution!
 */
function getRandomInt(min, max) {
    min = Math.ceil(min);
    max = Math.floor(max);
    return Math.floor(Math.random() * (max - min + 1)) + min;
}

function countPercentile(arr, target, diff){
    count = 0;
    for (var i =0; i < arr.length; i++){
        //console.log(Math.abs(arr[i] - target))
        if (Math.abs(arr[i] - target) < diff){
            count++;
        }
    }
    return count / arr.length;
}

function create ()
{
    function sendKeyPress (keypress)
    {
        const ints = new Int8Array(new ArrayBuffer(1));
        ints[0] = keypress;

        // TODO: we can probably handle this a bit better
        if (ws.readyState === WebSocket.OPEN) {
        ws.send(ints.buffer);
        }
    }

    // Set world bounds
    this.physics.world.setBounds(0, 0, 1600, 1200);
    console.log(this.sys.game.device.features);

    // sprites group for rendering sprites sent from server
    spritesGroup = this.add.group();

    // Add background player, enemy, reticle, healthpoint sprites
    var background = this.add.image(800, 600, 'background');
    reticle = this.add.sprite(800, 700, 'target');
    reticle.setScrollFactor(0);

    // create websocket connection
    host = window.location.hostname;
    protocol = "https:" === window.location.protocol ? "wss:" : "ws:";
    port = location.port;
    path = "/ws";
    ws = new WebSocket(protocol + host + ':' + port + path);
    ws.binaryType = "arraybuffer"; // needed for performance, very important.
    flag = true;
    playerid = 0;

    function printframebytes(view){
        n = view.byteLength;
        console.log("length:", n)
        a = decodeMsg(view);
        console.log("content:",a);
    }

    function decodeMsg(view){
        n = view.byteLength;
        bytearr = []
        for (var i = 0; i < n; i++){
            bytearr.push(view.getInt8(i));
        }
        if (n==1){
            return bytearr;
        }
        msgType = bytearr[0];
        switch(msgType){
            case 2: //player left
            case 5: //server full
                return bytearr;
            case 1: //player joined
            case 3: //current players list
            case 4: //state update
                a = [msgType];
                framesize = 9;
                offset = 1;
                numframes = (n-1)/framesize;
                for (var i = 0; i < numframes; i++){
                    index = i*framesize + offset;
                    id = view.getInt8(index);
                    xpos = view.getFloat32(index+1);
                    ypos = view.getFloat32(index+5);
                    a.push([id, xpos, ypos]);
                }
                return a;
        }
    }

    thiscontext = this;

    objmap = {}
    var lastrecv = performance.now();

    times = []
    ws.onmessage = function (event) {
            var elapsed = performance.now() - lastrecv;
            lastrecv = performance.now();
            times.push(elapsed);
            if (times.length === 100){
                sum = 0;
                for( var i = 0; i < times.length; i++ ){
                    sum += times[i];
                }
                console.log("elapsed:",times);
                console.log("average:",sum/times.length);
                console.log("maximum:",Math.max(...times));
                console.log("% within 0.5ms of target: ", countPercentile(times, 16, 0.5))
                console.log("% within 1ms of target: ", countPercentile(times, 16, 1))
                console.log("% within 2ms of target: ", countPercentile(times, 16, 2))
                times = []
            }
            var view = new DataView(event.data);
            printframebytes(view);
            msg = decodeMsg(view);
            msgType = msg[0];

            switch (msgType) {
                case 1: //new player joined
                    id = msg[1][0];
                    objmap[id] = spritesGroup.create(msg[1][1], msg[1][2], 'player_handgun').setDisplaySize(132, 120);
                    break;
                case 2: //player left
                    id = msg[1];
                    objmap[id].destroy();
                    break;
                case 3: //player list for new player
                    playerid = msg[1][0];
                    for (var i = 1; i < msg.length; i++) {
                        id = msg[i][0];
                        objmap[id] = spritesGroup.create(msg[i][1], msg[i][2], 'player_handgun').setDisplaySize(132, 120);
                    }
                    thiscontext.cameras.main.zoom = 0.5;
                    thiscontext.cameras.main.startFollow(objmap[playerid]);
                    gPlayer = objmap[playerid];
                    break;
                case 4: //state update
                    for (var i = 1; i < msg.length; i++) {
                        obj = msg[i];
                        //console.log("obj", obj);
                        [id, xpos, ypos] = obj;
                        objmap[id].x = xpos;
                        objmap[id].y = ypos;
                    }
                    break;
                case 5: //server full
                    console.log("Error: server full. Try again later.")
            }
    }

    // Set image/sprite properties
    background.setOrigin(0.5, 0.5).setDisplaySize(1600, 1200);
    reticle.setOrigin(0.5, 0.5).setDisplaySize(36, 36);

    // Creates object for input with WASD kets
    isKeyDown = {"w":false, "a":false, "s":false, "d":false}

    moveKeyEvents = ['keydown_W', 'keydown_S', 'keydown_A', 'keydown_D',
    'keyup_W', 'keyup_A', 'keyup_S', 'keyup_D']

    function sendKeyEvent() {
        var dx = 0, dy = 0;
        if (isKeyDown['a']){
            dx--;
        }
        if (isKeyDown['d']){
            dx++;
        }
        if (isKeyDown['w']){
            dy--;
        }
        if (isKeyDown['s']){
            dy++;
        }
        //console.log(1+(dx+1)+(dy+1)*3)
        sendKeyPress(1+(dx+1)+(dy+1)*3)
    }

    function sendKeyDown(key) {
        if (!isKeyDown[key]){
            isKeyDown[key] = true;
            sendKeyEvent();
        }
    }
    this.input.keyboard.on('keydown_W', function (event) {
        sendKeyDown("w");
    });
    this.input.keyboard.on('keydown_S', function (event) {
        sendKeyDown("s");
    });
    this.input.keyboard.on('keydown_A', function (event) {
        sendKeyDown("a");
    });
    this.input.keyboard.on('keydown_D', function (event) {
        sendKeyDown("d");
    });

    function sendKeyUp(key) {
        if (isKeyDown[key]){
            isKeyDown[key] = false;
            sendKeyEvent();
        }
    }
    // Stops player acceleration on uppress of WASD keys
    this.input.keyboard.on('keyup_W', function (event) {
        sendKeyUp("w");
    });
    this.input.keyboard.on('keyup_S', function (event) {
        sendKeyUp("s");
    });
    this.input.keyboard.on('keyup_A', function (event) {
        sendKeyUp("a");
    });
    this.input.keyboard.on('keyup_D', function (event) {
        sendKeyUp("d");
    });

    var randomToggle = false;
    var handle;
    this.input.keyboard.on('keydown_L', function (event) {
        // makes your character move randomly until you press l again
        if (!randomToggle){
            randomToggle = true;
            handle = window.setInterval(function(){
                x = getRandomInt(1,9);
                sendKeyPress(x);
            }, 100);
        } else {
            randomToggle = false;
            window.clearInterval(handle);
            sendKeyPress(5);
        }
    });

    // Pointer lock will only work after mousedown
    game.canvas.addEventListener('mousedown', function () {
        game.input.mouse.requestPointerLock();
    });

    // Move reticle upon locked pointer move
    this.input.on('pointermove', function (pointer) {
        if (this.input.mouse.locked)
        {
            reticle.x += pointer.movementX;
            reticle.y += pointer.movementY;
        }
    }, this);

}

// Ensures reticle does not move offscreen
function constrainReticle(reticle)
{
    // Ensures reticle cannot be moved offscreen (player follow)
    if (reticle.x < limits.left)
        reticle.x = limits.left;
    else if (reticle.x > limits.right)
        reticle.x = limits.right;

    if (reticle.y < limits.top)
        reticle.y = limits.top;
    else if (reticle.y > limits.bottom)
        reticle.y = limits.bottom;
}

function update (time, delta)
{
    // Constrain position of constrainReticle
    constrainReticle(reticle);
}
