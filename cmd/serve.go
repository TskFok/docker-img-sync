package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/TskFok/DockerImgSync/app/global"
	syncsvc "github.com/TskFok/DockerImgSync/service/sync"
	"github.com/TskFok/DockerImgSync/service/web"
	"github.com/TskFok/DockerImgSync/utils/database"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "启动 Web 服务和同步调度器",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result, err := database.EnsureTables(global.DataBase, global.MysqlPrefix)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		if len(result.Created) > 0 {
			fmt.Printf("已创建数据表: %s\n", strings.Join(result.Created, ", "))
		}
		if len(result.Altered) > 0 {
			fmt.Printf("已修改数据表: %s\n", strings.Join(result.Altered, ", "))
		}
		engine := syncsvc.NewCrane()
		syncStore := syncsvc.NewMySQLStore(global.DataBase, global.CredentialKey)
		if err := syncStore.BackfillLastSuccess(ctx); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		sched := syncsvc.NewScheduler(syncStore, engine)
		if err := sched.Start(ctx); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		store := web.NewMySQLStore(global.DataBase, global.CredentialKey)
		router := web.NewRouter(web.Deps{
			Store:         store,
			Registries:    store,
			Tasks:         store,
			Syncer:        sched,
			AdminUser:     global.AdminUsername,
			AdminPassword: global.AdminPassword,
			SessionSecret: global.SessionSecret,
		})
		if err := http.ListenAndServe(global.HTTPAddr, router); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
