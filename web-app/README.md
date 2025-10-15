# Web Application

A simple TypeScript web application built with Vite.

## Local Development

### Prerequisites

- Node.js (v18 or higher)
- npm

### Installation

```bash
# Install dependencies
npm install
```

### Development Servers

```bash
# Start development server
npm run dev
```

The application will be available at `http://localhost:5173`

### Build

```bash
# Build for production
npm run build
```

### Preview Production Build

```bash
# Preview production build locally
npm run preview
```

## Environment Variables

The application uses the following build-time environment variables:

| Variable                 | Description                                | Default |
| ------------------------ | ------------------------------------------ | ------- |
| `VITE_APP_BUILD_ID`      | Build identifier displayed in the app      | `dev`   |
| `VITE_APP_DEPLOYMENT_ID` | Deployment identifier displayed in the app | `local` |

## Docker

### Build Docker Image

Basic build with default values:

```bash
docker build -t web-app .
```

Build with custom environment variables:

```bash
docker build \
  --build-arg APP_BUILD_ID=v1.2.3 \
  --build-arg APP_DEPLOYMENT_ID=run-id-1234 \
  -t web-app .
```

Build using exported environment variables:

```bash
# Export variables
export APP_BUILD_ID="v1.0.0"
export APP_DEPLOYMENT_ID="run-id-1234"

# Build with exported variables
docker build \
  --build-arg APP_BUILD_ID="$APP_BUILD_ID" \
  --build-arg APP_DEPLOYMENT_ID="$APP_DEPLOYMENT_ID" \
  -t web-app .
```

### Run Docker Container

```bash
# Run on port 80 (default nginx port)
docker run -p 80:80 web-app

# Run on port 3000
docker run -p 3000:80 web-app
```
