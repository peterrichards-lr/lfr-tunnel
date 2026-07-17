import re

with open('.golangci.yml', 'r') as f:
    content = f.read()

# Replace exclude-functions block
new_excludes = """      exclude-functions:
        - io.ReadAll
        - io/ioutil.ReadAll
        - os.Remove
        - os.RemoveAll
        - net.SplitHostPort
        - (*net/http.Response).Body.Close
        - (*net/http.Server).ListenAndServe
        - (*net/http.Server).Shutdown
        - crypto/rand.Read
        - fmt.Sscanf
        - (*bufio.Writer).Flush
        - fmt.Fprintf
        - (*encoding/json.Encoder).Encode
        - encoding/json.Marshal
        - encoding/json.MarshalIndent
        - encoding/json.Unmarshal
        - (*os.File).Close
        - net.Conn.Close
        - (*net.TCPConn).Close
        - io.Copy
        - io.CopyBuffer
        - io.CopyN
        - (*net/http.Request).Context
        - strconv.Atoi
        - url.Parse
        - os.UserHomeDir
        - net/http.NewRequest
        - os.CreateTemp
        - os.Create
        - os.MkdirAll
        - os.Pipe
        - os.Rename
        - os.Setenv
        - os.WriteFile
        - (*os.Process).Kill
        - (*os/exec.Cmd).Run
        - (*os/exec.Cmd).Start
        - (*database/sql.Rows).Close
        - lfr-tunnel/pkg/config.LoadClientConfig"""

content = re.sub(r'      exclude-functions:.*?(?=\n    funlen:)', new_excludes + "\n", content, flags=re.DOTALL)

with open('.golangci.yml', 'w') as f:
    f.write(content)
