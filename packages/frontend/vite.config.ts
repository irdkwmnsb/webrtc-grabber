import { resolve } from 'path'
import { defineConfig } from 'vite'
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
            entry: [
                resolve(__dirname, 'lib/main.ts'),
                resolve(__dirname, 'pages/capture/capture.html'),
                resolve(__dirname, 'pages/index/index.html'),
                resolve(__dirname, 'pages/player/player.html')
            ],
            name: 'MyLib',
            // the proper extensions will be added
            fileName: 'my-lib',
        }
        // },
        // rollupOptions: {
        //     input: {
        //         capture: resolve(__dirname, 'pages/capture/capture.html'),
        //         index: resolve(__dirname, 'pages/index/index.html'),
        //         player: resolve(__dirname, 'pages/player/player.html'),
        //     }
        // }
    }
})
