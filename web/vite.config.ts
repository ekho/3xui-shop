import {defineConfig,type ViteDevServer,type PreviewServer} from 'vite';
import {readFileSync} from 'node:fs';
const fixture=readFileSync(new URL('./scripts/local-public-config.json',import.meta.url),'utf8');
function serveLocalConfig(server:ViteDevServer|PreviewServer){
 server.middlewares.use('/config.json',(_req,res)=>{res.setHeader('Content-Type','application/json');res.setHeader('Cache-Control','no-store');res.end(fixture);});
}
export default defineConfig({build:{sourcemap:false},plugins:[{name:'local-public-config',configureServer:serveLocalConfig,configurePreviewServer:serveLocalConfig}]});
