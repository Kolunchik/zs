# zs

```
package main

import (
        "fmt"
        "github.com/kolunchik/zs"
)

func main() {
        sender := zs.NewSender("localhost", 10051)
        response, err := sender.Send([]zs.ZabbixDataItem{
                {Host: "Zabbix server", Key: "trapper[test]", Value: "42"},
                {Host: "Zabbix server", Key: "trapper[test]", Value: "43"},
        })
        fmt.Println(response, err)
        ...
        responses, err := sender.SendBatch([]ZabbixDataItem{
                {Host: "Zabbix server", Key: "trapper[test]", Value: "42"},
                {Host: "Zabbix server", Key: "trapper[test]", Value: "42"},
                {Host: "Zabbix server", Key: "trapper[test]", Value: "42"},
                {Host: "Zabbix server", Key: "trapper[test]", Value: "43"},
                {Host: "Zabbix server", Key: "trapper[test]", Value: "43"},
                {Host: "Zabbix server", Key: "trapper[test]", Value: "43"},
                {Host: "Zabbix server", Key: "trapper[test]", Value: "43"},
        }, 3)
        fmt.Println(err)
        for i := range responses {
          fmt.Println(responses[i])
        }

}
```
