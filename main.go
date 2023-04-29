package main
import(
        "encoding/json"
        "time"
        "log"
        "net"
        "net/http"
        "sync"
        "errors"
	"github.com/jinzhu/gorm"
        _ "github.com/jinzhu/gorm/dialects/mysql"
        "github.com/go-redis/redis/v8"
        "github.com/gorilla/websocket"
)
type Message struct {
        Sender    string `json:"sender"`
        Receiver  string `json:"receiver"`
        Content   interface{} `json:"content"`
        Timestamp int64  `json:"timestamp"`
}

type HeartBeat struct {
        Health  string   `json:"health"`
}
type User struct {
        Email           string  `gorm:"primary_key`
        Password        string  `gorm:"not null"`
        Name            string  `gorm:"not null"`
        Gender          int     `gorm:"not null"`
        Age             int     `gorm:"not null"`
        Userid          string  `gorm:"unique;not null"`
        Teacher         string
}

type Teacher struct {
        Userid          string  `gorm:"primary_key"`
        Password        string  `gorm:"not null"`
}


/*
type Conn struct {
        ws *websocket.Conn
        timestamp int64
}
*/
var (
        redisClient *redis.Client
        connections sync.Map
        mapLock sync.RWMutex
        redisLock sync.RWMutex
        Database *gorm.DB
	upgrader = websocket.Upgrader{
                ReadBufferSize:  1024,
                WriteBufferSize: 1024,
                CheckOrigin: func(r *http.Request) bool {
                        return true
                },
        }
)

func initDatabase(){
        dsn := "root:12345678@tcp(172.16.20.10:3306)/cdt?charset=utf8mb4&parseTime=True&loc=Local"
        var err error
        Database, err = gorm.Open("mysql", dsn)
        if err != nil {
                log.Fatalf("failed to connect to database: %v", err)
        }
}
func FindUserByEmailAndPassword(table interface{}, token string, password string) error {
        return Database.Where("userid = ? AND password = ?", token, password).First(table).Error
}
func main(){
        // 初始化Redis客戶端
	initDatabase()
        defer Database.Close()
        redisClient = redis.NewClient(&redis.Options{
                Addr: "172.16.20.10:6379",
                Password: "12345678",
        })

        // 清空Redis中的使用者資訊和聊天記錄
        //redisClient.Del(redisClient.Context(),"users", "messages")
        go  handleMessages()

        // 設定HTTP請求路由
        http.HandleFunc("/test", handleIndex)
	http.HandleFunc("/teachertest", handleteacherIndex)
        http.HandleFunc("/ws", handleWebSocket(&User{}))
	http.HandleFunc("/teacherws", handleWebSocket(&Teacher{}))
        // 啟動WebSocket服務器
        //go handleMessages()
        log.Println("Server is listening on port 80...")
        log.Fatal(http.ListenAndServe(":80", nil))
}
// 處理HTTP GET請求，返回聊天室客戶端的HTML頁面
func handleIndex(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, "index.html")
}
func handleteacherIndex(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, "teacherindex.html")
}
func writeInRedis (userid string,msgBytes []byte){
	var health HeartBeat
        if err := json.Unmarshal(msgBytes, &health); err != nil {
                log.Println("Decode message error :", err)
                return
        }
        if health.Health != ""{
                //log.Println("Health :", userid)
                return
        }
        var msg Message
        if err := json.Unmarshal(msgBytes, &msg); err != nil {
                log.Println("Decode message error :", err)
                return
       	}
        log.Println("Message data :", msg.Sender, msg.Receiver, msg.Content)
        //確認寄件者
        if (msg.Sender != userid){
               	return
        }
               //添加時戳
        msg.Timestamp = NowUnix()
               //取出收件者
        redisLock.Lock()
        val, err := redisClient.HGet(redisClient.Context(), "messages", msg.Receiver).Result()
        if err != nil {
               	log.Println("redisGet users error :", err)
               	redisLock.Unlock()
               	return
        }
        //寫回Redis
        msgQueue := []Message{}
        if err := json.Unmarshal([]byte(val), &msgQueue); err != nil {
                log.Println("Decode error :", err)
                redisLock.Unlock()
                return
	}
	//log.Println("MsgQueue :", userid, ", Queuelen :",len(msgQueue))
        log.Printf("Value for key %s: %s\n", msg.Receiver, val)
        msgQueue = append(msgQueue,msg)
        data, err := json.Marshal(msgQueue)
        if err != nil {
               	log.Println("Encode error :", err)
               	redisLock.Unlock()
               	return 
        }
        if err := redisClient.HSet(redisClient.Context(), "messages", msg.Receiver, data).Err(); err != nil {
               	log.Println("redisGet messages error :", err)
               	redisLock.Unlock()
               	return
        }
        redisLock.Unlock()
}
func identificationCheck(table interface{}, idBytes []byte) (string, error) {
        identification := &struct {
                Sender string `json:"sender"`
                Password string `json:"password"`
        }{}
        if err := json.Unmarshal(idBytes, &identification); err != nil {
                //log.Println("Decode ID error:", err)
        	return "", err

        }
        //log.Println("identification verify :",identification.Sender)
        ///////////////////////////////////////檢查身分/////////
        if err := FindUserByEmailAndPassword(table, identification.Sender, identification.Password); err != nil {
              	return "", err
        }
        return identification.Sender,nil
}
func initUserMsg(userid string) error{
	executionMachineIP, err := getIP()
        if err != nil {
                log.Println("GetIP error:,err")
                return err
        }
        log.Println("executionMachineIP verify :",executionMachineIP)
        if prevWs, ok := connections.Load(userid); ok {
                if prevConn, ok := prevWs.(*websocket.Conn); ok && prevConn != nil{
                        prevConn.Close()
                        log.Println("Close prevConn :",userid)
                }
        }
        if err := redisClient.HSet(redisClient.Context(), "users", userid, executionMachineIP).Err(); err != nil {
                log.Println("Redis Set error :", err)
                return err
        }
        if _, err := redisClient.HGet(redisClient.Context(), "messages", userid).Result(); err != nil {
                initMsgQueue, err := json.Marshal(&[]Message{})
	        if err != nil {
                        log.Println("Encode error :", err)
                        return err
                }
                if err := redisClient.HSet(redisClient.Context(), "messages", userid, initMsgQueue).Err(); err != nil {
                        log.Println("Redis Set error :", err)
                        return err
                }
        }
	return nil
}
func serverWs(w http.ResponseWriter, r *http.Request, identity interface{}){
	ws, err := upgrader.Upgrade(w, r, nil)
        defer ws.Close()
        if err != nil {
                return
        }
        // 讀取客戶端的ID
        _, idBytes, err := ws.ReadMessage()
        if err != nil {
                log.Println("Ws Error")
                return
        }
        // 檢查身分
	userid, err := identificationCheck(identity, idBytes);
        if err != nil {
                return
        }
        //用戶資料設定
        if err := initUserMsg(userid); err != nil {
                return
        }
        mapLock.Lock()
        connections.Store(userid, ws)
        mapLock.Unlock()
        for {
                _, msgBytes, err := ws.ReadMessage()
                if err != nil {
                        log.Println("ReadMessage error :", err)
                        break
                }
                go writeInRedis(userid,msgBytes)
        }
        //移除MAP
        mapLock.Lock()
        connections.Delete(userid)
        log.Println("Map Delete :",userid)
        mapLock.Unlock()
}
func handleWebSocket(identity interface{}) func(http.ResponseWriter, *http.Request) {
    return func(w http.ResponseWriter, r *http.Request) {
        serverWs(w, r, identity)
    }
}

func handleMessages() {
        msgExecutionMachineIP, err:= getIP()
        if err != nil {
                return
        }
        for {
                mapLock.Lock()
                connections.Range(func(key, value interface{}) bool {
                        keyString := key.(string)
                        executionMachineIP, err := redisClient.HGet(redisClient.Context(), "users", keyString).Result()
                        if err != nil {
                                log.Println("Msg redisGet users error :", err)
                                return true
                        }
                        //var executionMachineIP string
                        conn := value.(*websocket.Conn)
                        if executionMachineIP != msgExecutionMachineIP {
                                conn.Close()
                                log.Println("Msg Decode execMachIP error :", executionMachineIP, " = ", msgExecutionMachineIP)
                                return true
                        }
                        redisLock.Lock()
                        msgBytes, err := redisClient.HGet(redisClient.Context(), "messages", keyString).Result()
                        if err != nil {
                                //log.Println("Msg redisGet messages error :", err)
                                redisLock.Unlock()
                                return true
                        }
                        msgQueue := []Message{}
                        if err := json.Unmarshal([]byte(msgBytes), &msgQueue); err != nil {
                                log.Println("Msg Decode msgQueue error :", err)
                                redisLock.Unlock()
                                return true
                        }
                        for _, msg := range msgQueue{
                                msgData, err := json.Marshal(msg)
                                if err != nil {
                                        log.Println("Msg Encode message error:", err)
                                        continue
                                }
                                if err := conn.WriteMessage(websocket.TextMessage, msgData); err != nil{
                                        log.Println("Ws error :", err)
                                }
                                //log.Println("MsgQueue :", keyString, ", Queuelen :",len(msgQueue))
                        }
                        //log.Println("MsgQueue :", keyString, ", Queuelen :",len(msgQueue))
                        msgQueue = make([]Message, 0)
                        data, err := json.Marshal(msgQueue)
                        if err != nil {
                                log.Println("Msg Encode msgQueue error :", err)
                                redisLock.Unlock()
                                return true
                        }
                        if err := redisClient.HSet(redisClient.Context(), "messages", keyString, data).Err(); err != nil {
                                log.Println("Msg redisSet messages error :", err)
                                redisLock.Unlock()
                                return true
                        }
                        redisLock.Unlock()
                        return true
                })
                mapLock.Unlock()
        }
}
func NowUnix() int64 {
    return time.Now().Unix()
}
func getIP() (string ,error){
        addrs, err := net.InterfaceAddrs()
        if err != nil {
                return "" ,err
        }
        subnet := net.IPNet{
                IP:   net.ParseIP("172.16.0.0"),
                Mask: net.CIDRMask(16, 32),
        }
        for _, addr := range addrs {
                if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
                        if subnet.Contains(ipNet.IP) {
                                return ipNet.IP.String(), nil
                        }
                }
        }
        return "", errors.New("Not get IP")
}

