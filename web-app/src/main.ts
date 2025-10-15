import './style.css';
import typescriptLogo from '/typescript.svg';
import viteLogo from '/vite.svg';
import { setupCounter } from './counter.ts';

const appBuildId = import.meta.env.VITE_APP_BUILD_ID || 'dev';
const appDeploymentId = import.meta.env.VITE_APP_DEPLOYMENT_ID || 'local';

document.querySelector<HTMLDivElement>('#app')!.innerHTML = `
  <div>
    <a href="https://vite.dev" target="_blank">
      <img src="${viteLogo}" class="logo" alt="Vite logo" />
    </a>
    <a href="https://www.typescriptlang.org/" target="_blank">
      <img src="${typescriptLogo}" class="logo vanilla" alt="TypeScript logo" />
    </a>
    <h1>HelloWorld</h1>
    <div class="card">
      <button id="counter" type="button"></button>
    </div>
    <p class="read-the-docs">
      Click on the Vite and TypeScript logos to learn more
    </p>
    <p class="read-the-docs">
      Build ID: ${appBuildId} | Deployment ID: ${appDeploymentId}
    </p>
  </div>
`;

setupCounter(document.querySelector<HTMLButtonElement>('#counter')!);
