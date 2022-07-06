// Modules to control application life and create native browser window
const { app, BrowserWindow, desktopCapturer } = require("electron");
const {
    hasScreenCapturePermission,
    hasPromptedForPermission
} = require('mac-screen-capture-permissions');

const path = require("path");

const signalingUrl = "http://localhost:3000";
const name = "Tester";
let mainWindow = null;

async function createWindow() {
    // Create the browser window.
    mainWindow = new BrowserWindow({
        width: 1000,
        height: 900,
        webPreferences: {
            preload: path.join(__dirname, "preload.js")
        }
    });

    mainWindow.on('close', (event) => {
        if (app.quitting) {
            mainWindow = null
        } else {
            event.preventDefault()
            mainWindow.hide()
        }
    })

    // and load the index.html of the app.
    await mainWindow.loadURL(`file://${ __dirname}/index.html`)
    // await mainWindow.toggleTabBar();

    // Open the DevTools.
    mainWindow.webContents.openDevTools();

    console.log(hasPromptedForPermission());

    console.log(hasScreenCapturePermission());

    desktopCapturer.getSources({ types: ['screen'] }).then(async sources => {
        for (const source of sources) {
            mainWindow.webContents.send('SET_SOURCE', source.id)
        }
    })
    mainWindow.webContents.send("START", signalingUrl, name);
}

// This method will be called when Electron has finished
// initialization and is ready to create browser windows.
// Some APIs can only be used after this event occurs.
app.whenReady().then(async () => {
    await createWindow();

    app.on("activate", function () {
        // On macOS it's common to re-create a window in the app when the
        // dock icon is clicked and there are no other windows open.
        if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
});

app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') {
        app.quit()
    }
})

app.on('activate', () => { mainWindow.show() })

app.on('before-quit', () => app.quitting = true)

// In this file you can include the rest of your app's specific main process
// code. You can also put them in separate files and require them here.
