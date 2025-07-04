
# GEMINI Project Analysis: New API

## Project Overview

**New API** is a next-generation AI gateway and asset management system, forked from the popular `One API` project. It aims to provide a more powerful, feature-rich, and user-friendly experience for developers and organizations that utilize various large language models (LLMs). The system acts as a unified interface for a multitude of AI models, offering advanced features like intelligent routing, comprehensive billing, and enhanced security.

The project is built with a modern technology stack, featuring a **Go** backend for high performance and a completely redesigned frontend for an improved user experience. It is designed for easy deployment using **Docker**, making it accessible for both individual developers and large-scale enterprise environments.

## Technology Stack

### Backend

- **Language:** Go (version 1.23.4 or higher)
- **Web Framework:** Gin
- **ORM:** GORM
- **Database Support:**
    - MySQL (recommended for production)
    - PostgreSQL
    - SQLite (default for simple deployments)
- **Caching:** Redis (for performance and caching query results)
- **Key Dependencies:**
    - `gin-gonic/gin`: For handling HTTP requests.
    - `gorm.io/gorm`: For database interactions.
    - `go-redis/redis`: For Redis caching.
    - `tiktoken-go/tokenizer`: For token counting.
    - `golang-jwt/jwt`: For authentication.

### Frontend

- **Location:** `/web` directory
- **JavaScript Runtime/Bundler:** Bun
- **UI:** A completely new user interface, likely built with a modern JavaScript framework like React or Vue (inferred from the use of `VITE_REACT_APP_VERSION` in the `makefile`).

### DevOps & Deployment

- **Containerization:** Docker and Docker Compose
- **Build Automation:** `makefile` for building the frontend and backend for various platforms (Linux, Windows, macOS).
- **Continuous Integration:** The presence of a `.github/workflows` directory suggests the use of GitHub Actions for CI/CD.

## Key Features & Functionality

- **Unified API Gateway:** Provides a single endpoint to access a wide variety of LLMs from different providers.
- **Advanced Model Support:**
    - Standard models from OpenAI, Azure, etc.
    - Specialized models like **gpts** (gpt-4-gizmo-*), **Rerank models** (Cohere, Jina), and models supporting **Claude Messages format**.
    - Integration with third-party services like **Midjourney-Proxy** and **Suno API**.
- **Enhanced Billing & Quota Management:**
    - **Online Payments:** Supports online top-ups via Epay.
    - **Flexible Billing:** Offers billing per request in addition to token-based billing.
    - **Quota Inquiry:** Allows users to check their usage quota using an API key.
    - **Cached Billing:** Supports billing for cached responses at a reduced rate.
- **Intelligent Routing & Load Balancing:**
    - **Weighted Random:** Implements weighted random routing for channels to balance the load.
    - **Channel Retries:** Automatically retries failed requests on different channels.
- **Improved User Experience:**
    - **Modern UI:** A completely redesigned, user-friendly web interface.
    - **Data Dashboard:** A console for monitoring usage and system status.
    - **Multi-language Support:** Caters to a global user base.
- **Security & Access Control:**
    - **Token Management:** Advanced token grouping and model restriction capabilities.
    - **Multiple Login Methods:** Supports authentication via LinuxDO, Telegram, and OIDC.
- **High Performance:**
    - **Go Backend:** The use of Go ensures high performance and concurrency.
    - **Caching:** Leverages Redis for in-memory caching to reduce latency.

## Project Structure

The project follows a well-organized, modular structure that separates concerns, making it easier to maintain and extend:

- `main.go`: The application's entry point.
- `/controller`: Contains HTTP handlers that process incoming requests.
- `/model`: Defines the GORM data models for database tables.
- `/service`: Encapsulates the core business logic.
- `/router`: Defines the API routes and connects them to the appropriate controllers.
- `/middleware`: Implements Gin middleware for tasks like authentication, logging, and rate limiting.
- `/common`: Holds shared utility functions, constants, and configurations.
- `/relay`: Contains the logic for adapting and relaying requests to various downstream AI services.
- `/web`: The source code for the frontend application.
- `docker-compose.yml`: Defines the services, networks, and volumes for Docker-based deployment.
- `makefile`: Provides convenient commands for building, running, and managing the project.

This structure promotes a clean separation of concerns and is typical for a modern Go web application.
