// auth/client.go
package auth

import (
	"context"
	"log"
	"strings"
	"synxrondrive/pkg/proto/auth_v1"
)

// UserInfo представляет информацию о пользователе в нашем приложении
type UserInfo struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Lastname string `json:"lastname"`
	Photo    string `json:"photo"`
}

func GetUsersByIds(ctx context.Context, userIds []string) ([]UserInfo, error) {
	// Убираем пустые значения и дубликаты
	cleanIds := make([]string, 0)
	idMap := make(map[string]bool)

	for _, id := range userIds {
		id = strings.TrimSpace(id)
		if id != "" && !idMap[id] {
			cleanIds = append(cleanIds, id)
			idMap[id] = true
		}
	}

	if len(cleanIds) == 0 {
		return nil, nil
	}

	// Делаем запрос к auth сервису
	response, err := gClient.GetUsersByIds(ctx, &auth_v1.GetUsersByIdsRequest{
		Ids: cleanIds,
	})
	if err != nil {
		log.Printf("Error getting users from auth service: %v", err)
		return nil, err
	}

	// Преобразуем ответ в нужный формат
	users := make([]UserInfo, 0, len(response.Users))
	for _, user := range response.Users {
		users = append(users, UserInfo{
			ID:       user.Id,
			Email:    user.Email,
			Name:     user.Name,
			Lastname: user.Lastname,
			Photo:    user.Photo,
		})
	}

	return users, nil
}
