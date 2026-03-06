#!/bin/bash

# DecentChat Quick Start Script
# This script helps you set up and run DecentChat

set -e

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║                   DecentChat Setup Script                   ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# Check for Go
if ! command -v go &> /dev/null; then
    echo "❌ Go is not installed. Please install Go 1.21 or higher."
    echo "   Visit: https://golang.org/doc/install"
    exit 1
fi

echo "✓ Go version: $(go version)"
echo ""

# Check for environment variables
if [ -z "$SUPABASE_URL" ] || [ -z "$SUPABASE_KEY" ]; then
    echo "⚠️  Supabase environment variables not set!"
    echo ""
    echo "Please set the following environment variables:"
    echo "  export SUPABASE_URL='https://your-project.supabase.co'"
    echo "  export SUPABASE_KEY='your-anon-key'"
    echo ""
    echo "You can find these values in your Supabase project settings."
    echo ""
    
    # Check for .env file
    if [ -f ".env" ]; then
        echo "Loading from .env file..."
        source .env
    else
        echo "Creating .env template..."
        cat > .env << EOF
# Supabase Configuration
# Get these from: https://supabase.com/dashboard/project/YOUR_PROJECT/settings/api
SUPABASE_URL=https://your-project.supabase.co
SUPABASE_KEY=your-anon-key-here
EOF
        echo "Created .env template. Please edit it with your Supabase credentials."
        exit 1
    fi
fi

# Navigate to project directory
cd "$(dirname "$0")"

# Download dependencies
echo "📦 Downloading dependencies..."
go mod tidy

# Build the application
echo "🔨 Building DecentChat..."
go build -o decentchat ./cmd

echo ""
echo "✅ Build complete!"
echo ""
echo "─────────────────────────────────────────────────────────────────"
echo ""
echo "To run DecentChat:"
echo "  ./decentchat"
echo ""
echo "For help with commands, type /help in the application."
echo ""
echo "─────────────────────────────────────────────────────────────────"
