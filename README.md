# 🌲 TourHub Tour Booking API

A high-performance, concurrent RESTful API for tour bookings built with **Go**, **Gin Web Framework**, and **MongoDB**. Designed with role-based access control, geospatial search capabilities, caching, and integrated payment workflows.

---

## 🚀 Features

* **Authentication & Authorization:** Secure JWT-based authentication with token blacklisting, password reset flows, and fine-grained role-based access control (`admin`, `lead-guide`, `guide`, `user`).
* **Geospatial Engine:** Find tours within a specific radius and calculate distances using MongoDB `$geoNear` and `$geoWithin` spatial index queries.
* **Rate Limiting:** Context-aware rate limiting across public, protected, and administrative endpoints.
* **Payment Integration:** Seamless integration with Paystack payment gateway (checkout sessions, payment verification, and webhooks).
* **Automated Swagger Documentation:** Complete OpenAPI 2.0 / Swagger UI documentation auto-generated with `swag`.
* **Containerized Deployment:** Fully dockerized setup for seamless environment replication.

---

## 🛠️ Tech Stack

* **Language:** Go 1.22+
* **Framework:** [Gin Gonic](https://github.com/gin-gonic/gin)
* **Database:** MongoDB Go Driver
* **Caching:** In-memory Token Blacklist Cache
* **Documentation:** Swaggo (`swag`) & `gin-swagger`
* **Containerization:** Docker & Docker Compose

---

## 📂 Project Structure

```text
├── cmd/
│   └── main.go                 # Application entry point & router setup
├── docs/                       # Auto-generated Swagger documentation
├── internal/
│   ├── config/                 # Environment and app configurations
│   ├── controllers/            # Route HTTP handlers
│   ├── middleware/             # Auth, Rate Limiter, Error handling, Uploads
│   └── models/                 # MongoDB schema data structs
├── pkg/
│   ├── cache/                  # Token blacklist cache management
|   ├── email/                  # mail service implementations
|   ├── paystack/               # Paystack service implementations
│   └── utils/                  # Utils service implementations
├── .env.example                # Environment Variables
├── public/                     # Uploaded static assets (tour images)
├── Dockerfile                  # Multi-stage Docker image build
├── docker-compose.yml          # Container orchestration setup
└── go.mod                      # Go module dependencies
``` 
---

## 🛠️ Installation

### 1. Clone the repository

```bash
git clone https://github.com/paularinzee/tour-booking-api.git
cd tour-booking-api
```

### 2. Install dependencies
```bash
go mod download
go mod tidy
```

### 3. Set up environment variables
Create a .env file in the project root:

```env
# Database Configuration
PORT=8080
MONGODB_URI=mongodb://localhost:27017
DB_NAME=tourhub

# Paystack Configuration
PAYSTACK_SECRET_KEY=sk_test_xxx
# Mock mode (set to true for testing without real Paystack)
PAYSTACK_MOCK_MODE=false
# JWT Configuration
JWT_SECRET=your-secret-key-change-this

```

### 4. Run the Application:
Start the server:

```
go run cmd/main.go

```
The server will start on port 8080 by default. You can access the API at http://localhost:8080.

--- 