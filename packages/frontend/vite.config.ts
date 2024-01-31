import {resolve} from 'path'
import {defineConfig} from 'vite'
import checker from 'vite-plugin-checker'

export default defineConfig({
    plugins: [
        checker({
            // e.g. use TypeScript check
            typescript: true,
        }),
    ],
    build: {
        lib: {
            // Could also be a dictionary or array of multiple entry points
            entry: {
                sdk: resolve(__dirname, 'lib/main.ts'),
                capture: resolve(__dirname, 'pages/capture/capture.html'),
                index: resolve(__dirname, 'pages/index/index.html'),
                player: resolve(__dirname, 'pages/player/player.html')
            },
            name: 'webrtc-grabber-web',
            // the proper extensions will be added
            fileName: 'webrtc-grabber-[name]',
            formats: ['es', 'cjs']
        }
    }
})
