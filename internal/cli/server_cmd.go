package cli

import (
 "os"
 "github.com/spf13/cobra"
 "github.com/Ozzmosis94/raygate/internal/web"
)

func newServerCmd() *cobra.Command {
 return &cobra.Command{
  Use:"server",
  Short:"Run the authenticated Web API and proxy dashboard",
  Args:cobra.NoArgs,
  RunE:func(cmd *cobra.Command,_ []string) error {
   if homeDir!=""{if err:=os.Setenv("RAYGATE_HOME",homeDir);err!=nil{return err}}
   return web.Serve(cmd.Context())
  },
 }
}
