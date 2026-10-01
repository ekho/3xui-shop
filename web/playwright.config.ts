import { defineConfig } from '@playwright/test';
export default defineConfig({testDir:'./tests',timeout:20000,expect:{timeout:2000},workers:1,fullyParallel:false,reporter:'line',use:{baseURL:'http://127.0.0.1:4173',trace:'off',screenshot:'off'},webServer:{command:'npm run build -- --mode test && npm run preview',url:'http://127.0.0.1:4173',reuseExistingServer:false,timeout:60000}});
