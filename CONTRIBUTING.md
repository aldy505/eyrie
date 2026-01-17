# Contribution Guide

Thank you for considering to contribute to eyrie.

## Development Setup

You would need:
- Go programming language installed (preferrably version 1.25 or higher)
- Node.js installed (preferably version 22.x or higher)
- An IDE of choice (VSCode, JetBrains GoLand, or Vim)

If you have a good technical skill of frontend development, you may swap
Node.js with Bun or any other sophisticated Javascript runtime to run Vite.

## Running locally

To run this, you need to spawn a few terminals. Start with running the server:
```bash
go run . -mode=server -config=./example_configurations/server.example.yaml -monitor=./example_configurations/monitor.example.yaml
```

Then run each checker nodes. You may run these on a single machine, but it
would be best if you have multiple computers to run this.
```bash
UPSTREAM_URL="http://localhost:8600" REGION="us-east-1" API_KEY="us-east-1-api-key-here" go run . -mode=checker

UPSTREAM_URL="http://localhost:8600" REGION="us-west-1" API_KEY="us-west-1-api-key-here" go run . -mode=checker

UPSTREAM_URL="http://localhost:8600" REGION="eu-west-1" API_KEY="eu-west-1-api-key-here" go run . -mode=checker

UPSTREAM_URL="http://localhost:8600" REGION="eu-central-1" API_KEY="eu-central-1-api-key-here" go run . -mode=checker

UPSTREAM_URL="http://localhost:8600" REGION="ap-southeast-1" API_KEY="ap-southeast-1-api-key-here" go run . -mode=checker
```

Then you may run the frontend separately by running:
```bash
cd frontend
npm install

npm run dev
```