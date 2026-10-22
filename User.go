package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

const (
	userIDContextKey = "authenticated_user_id"
	jwtIssuer        = "duitin-api"
	jwtLifetime      = 24 * time.Hour
)

type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type registerUserInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginUserInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userClaims struct {
	jwt.RegisteredClaims
}

func jwtSecretFromEnv() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 bytes")
	}
	return []byte(secret), nil
}

func Users(router *gin.Engine, db *sql.DB, secret []byte) {
	router.POST("/auth/register", registerUser(db))
	router.POST("/auth/login", loginUser(db, secret))
	router.GET("/users/me", authMiddleware(secret), getCurrentUser(db))
}

func authMiddleware(secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid bearer token"})
			return
		}

		claims := &userClaims{}
		token, err := jwt.ParseWithClaims(parts[1], claims, func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, errors.New("unexpected JWT signing method")
			}
			return secret, nil
		}, jwt.WithIssuer(jwtIssuer), jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		userID, err := strconv.ParseInt(claims.Subject, 10, 64)
		if err != nil || userID < 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token subject"})
			return
		}
		c.Set(userIDContextKey, userID)
		c.Next()
	}
}

func registerUser(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input registerUserInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		input.Name = strings.TrimSpace(input.Name)
		input.Email = normalizeEmail(input.Email)
		if message := validateRegisterUserInput(input); message != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": message})
			return
		}

		passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("hash registration password: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not register user"})
			return
		}

		var user User
		err = db.QueryRowContext(c.Request.Context(),
			`INSERT INTO users (name, email, password_hash)
			 VALUES ($1, $2, $3)
			 RETURNING id, name, email, created_at, updated_at`,
			input.Name, input.Email, string(passwordHash),
		).Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt, &user.UpdatedAt)
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" {
				c.JSON(http.StatusConflict, gin.H{"error": "email is already registered"})
				return
			}
			log.Printf("register user: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not register user"})
			return
		}
		c.JSON(http.StatusCreated, user)
	}
}

func loginUser(db *sql.DB, secret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input loginUserInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		input.Email = normalizeEmail(input.Email)
		if input.Email == "" || input.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email and password are required"})
			return
		}

		var user User
		var passwordHash string
		err := db.QueryRowContext(c.Request.Context(),
			`SELECT id, name, email, password_hash, created_at, updated_at
			 FROM users
			 WHERE email = $1`,
			input.Email,
		).Scan(&user.ID, &user.Name, &user.Email, &passwordHash, &user.CreatedAt, &user.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.Password)) != nil) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid email or password"})
			return
		}
		if err != nil {
			log.Printf("get user for login: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not log in"})
			return
		}

		now := time.Now()
		expiresAt := now.Add(jwtLifetime)
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, userClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    jwtIssuer,
				Subject:   strconv.FormatInt(user.ID, 10),
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(expiresAt),
			},
		}).SignedString(secret)
		if err != nil {
			log.Printf("sign login token: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not log in"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"token":      token,
			"token_type": "Bearer",
			"expires_at": expiresAt,
			"user":       user,
		})
	}
}

func getCurrentUser(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt64(userIDContextKey)
		var user User
		err := db.QueryRowContext(c.Request.Context(),
			`SELECT id, name, email, created_at, updated_at
			 FROM users
			 WHERE id = $1`,
			userID,
		).Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt, &user.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		if err != nil {
			log.Printf("get current user %d: %v", userID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not get user"})
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateRegisterUserInput(input registerUserInput) string {
	if input.Name == "" {
		return "name is required"
	}
	if utf8.RuneCountInString(input.Name) > 100 {
		return "name must be at most 100 characters"
	}
	if len(input.Email) == 0 || len(input.Email) > 255 {
		return "email must be between 1 and 255 characters"
	}
	address, err := mail.ParseAddress(input.Email)
	if err != nil || address.Address != input.Email {
		return "email must be a valid email address"
	}
	if len(input.Password) < 8 || len(input.Password) > 72 {
		return "password must be between 8 and 72 bytes"
	}
	return ""
}
