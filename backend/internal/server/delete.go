package server

import (
 "context"
 "errors"
 "log/slog"
 "net/http"
 "strings"
 "dnd-bot/backend/internal/store"
 "github.com/go-chi/chi/v5"
 "github.com/jackc/pgx/v5/pgconn"
)

func(s *Server)deleteRoom(w http.ResponseWriter,r *http.Request){
 var in struct {Code string `json:"code"`}
 if err:=decode(w,r,&in);err!=nil {fail(w,err);return}
 if err:=s.deleteCampaign(r.Context(),chi.URLParam(r,"id"),uid(r),in.Code);err!=nil{fail(w,err);return}
 respond(w,http.StatusOK,map[string]bool{"deleted":true})
}

func(s *Server)deleteCampaign(ctx context.Context,rid string,user int64,confirmation string)error{
 // Check ownership before trying a row lock so non-owners cannot probe activity.
 room,err:=s.load(ctx,rid,user);if err!=nil{return err}
 if !CanManage(room.OwnerID,user){return denied()}
 if strings.ToUpper(strings.TrimSpace(confirmation))!=room.Code {
  return bad("Для удаления введи код комнаты. Персонажи и вся история будут удалены.")
 }
 tx,err:=s.Pool.Begin(ctx);if err!=nil{return err};defer tx.Rollback(context.Background())
 q:=store.New(tx);roomID,_:=id(rid)
 raw,err:=q.LockRoomNowait(ctx,roomID)
 if err!=nil {var pgerr *pgconn.PgError;if errors.As(err,&pgerr)&&pgerr.Code=="55P03"{return &apiError{409,"ROOM_BUSY","Дождись завершения текущего хода и повтори удаление."}};return err}
 if !CanManage(raw.OwnerID,user){return denied()}
 if err=q.DeleteRoom(ctx,roomID);err!=nil{return err}
 if err=tx.Commit(ctx);err!=nil{return err}
 s.Hub.DeleteRoom(rid)
 slog.Info("room deleted","room",rid,"owner",user)
 return nil
}
