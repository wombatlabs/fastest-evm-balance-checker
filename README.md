# Balance checker for EVM networks
**12 networks, 1000 wallets, 5 seconds**

https://github.com/B0R9F3D9/fastest-evm-balance-checker/assets/131712860/b8ae5772-3586-43c9-827d-42b1fff889b2

# Available Networks
**Arbitrum, Arbitrum Nova, Base, Blast, BSC, Ethereum, Fantom, Linea, Optimism, Polygon, Scroll, Zora. However, you can add any EVM network**

# Настройка
* In the file `wallets.txt` we enter wallet **addresses** on a new line
  
# Установка
#### *To ensure everything is displayed correctly, it is better to use VS Code or Windows Terminal*
* [Download and install Golang](https://go.dev/dl/)

Open cmd and write:
1. `cd path/to/project` 
2. `go mod download` 

# Запуск
```
go run .
```
Or, if you wish, you can first build the project and then open the .exe file (nothing will change, the above option is easier)
```
go build .
```

###### P.S. Of course, you can make a checker even faster than this, but I just wanted to test go
