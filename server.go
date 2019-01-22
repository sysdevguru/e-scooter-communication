package tcp

import (
	"bufio"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const mongoDBHost = "127.0.0.1:27017"

// registerScooter registers scooter when connects first or reconnect
func registerScooter(imei string) string {
	mongoSession, err := mgo.Dial(mongoDBHost)
	if err != nil {
		log.Println("Error Connecting MongoDBHost")
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	defer mongoSession.Close()

	c := mongoSession.DB("lime").C("lock")
	var lock common.Lock
	lock.LockID = imei
	lock.Instruction = ""
	err = c.Find(bson.M{"lockid": imei}).One(&lock)
	if err != nil {
		if err.Error() == "not found" {
			err = c.Insert(&lock)
			if err != nil {
				return "Error"
			}
		}
		return "Success"
	}
	return "Exists"
}

// handleRequestFromIoT handles the commands from IoT to server
func handleRequestFromIoT(conn net.Conn) {
	defer conn.Close()

	// First, connect to DB that has all the scooter imei informations
	mongoSession, err := mgo.Dial(mongoDBHost)
	if err != nil {
		log.Println("Error Connecting MongoDBHost")
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	defer mongoSession.Close()
	c := mongoSession.DB("lime").C("lock")

	for {
		// Read message from IoT
		message, _ := bufio.NewReader(conn).ReadString('\n')
			
		if strings.TrimSpace(message) != "" {
			// Parse message from IoT
			arr := strings.Split(message, ",")
			imei := arr[2]
			inst := arr[3]
				
			// Register
			if inst == "Q0" {
				result := registerScooter(imei)
				if result == "Error" {
					log.Println("Error while saving imei")
				}
				// Setting scooter with default setting condition
				instruction := "*SCOS,OM,"
				instruction += imei
				/*
					Setting all scooters as follows
					Accelerometer sensitivity : middle
					Unlock status info upload : On
					Heartbeat upload interval : 240s
					Unlock status info uploading interval : 10s
				*/
				instruction += ",S5,2,2,10,10"
				instruction += "#"

				arr := util.MakeCMD(instruction)
				conn.Write(arr)
			}

			// Heartbeat
			if inst == "H0" {
				var lock common.Lock
				err = c.Find(bson.M{"lockid": imei}).One(&lock)
				if err != nil {
					log.Println("imei Not Found")
				}
				scooterStatus := arr[4]
				driveVolt := arr[5]
				networkSignal := arr[6]
				power := arr[7]
				chargingStatus := arr[8]

				lock.Power = power + "%"
				lock.ScooterStatus = util.ScooterStatus(scooterStatus)
				lock.DriveVolt = util.ConvertVoltage(driveVolt)
				lock.NetworkSignal = networkSignal
				lock.ChargingStatus = util.ChargingStatus(chargingStatus)

				// Update lock in DB
				colQuerier := bson.M{"lockid": lock.LockID}
				changeStatus := bson.M{"$set": bson.M{"power":lock.Power, "scooterstatus":lock.ScooterStatus, "drivervolt":lock.DriveVolt, "networksignal":lock.NetworkSignal, "chargingstatus":lock.ChargingStatus}}
				_ = c.Update(colQuerier, changeStatus)
			}
		}
	}
}

// handleRequestFromClient handles the commands that triggers at first from server to IoT
func handleRequestFromClient(conn net.Conn) {
	defer conn.Close()

	mongoSession, err := mgo.Dial(mongoDBHost)
	if err != nil {
		log.Println("Error Connecting MongoDBHost")
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	defer mongoSession.Close()
	c := mongoSession.DB("lime").C("lock")

	for {
		var imei string
		// Read message from IoT
		message, _ := bufio.NewReader(conn).ReadString('\n')
					
		if strings.TrimSpace(message) != "" {
			// Parse message from IoT
			arr := strings.Split(message, ",")
			imei = arr[2]
			inst := arr[3]
			oper := arr[4]
			var key string

			if inst == "R0" {
				key = arr[5]
				instruction := "*SCOS,OM,"
				instruction += imei
				
				if oper == "1" {
					instruction += ",L1,"
					instruction += key	
					instruction += "#"
				} else {
					timestamp := util.MakeTimestamp()
					instruction += ",L0,"
					//instruction += "0,"
					instruction += key
					instruction += ",0,"
					instruction += timestamp
					instruction += "#"
				}
				
				arr := util.MakeCMD(instruction)
				conn.Write(arr)
			}

			if inst == "L0" {
				if oper == "0" {
					instruction := "*SCOS,OM,"
					instruction += imei
					instruction += ",L0"
					instruction += "#"
					
					arr := util.MakeCMD(instruction)
					conn.Write(arr)

					// set "Done" as the instruction in the DB
					colQuerier := bson.M{"lockid": imei}
					changeStatus := bson.M{"$set": bson.M{"lockid": imei, "instruction": "Done"}}
					_ = c.Update(colQuerier, changeStatus)
				} else {
					// set "Fail" as the instruction in the DB
					colQuerier := bson.M{"lockid": imei}
					changeStatus := bson.M{"$set": bson.M{"lockid": imei, "instruction": "Fail"}}
					_ = c.Update(colQuerier, changeStatus)
				}
			}

			if inst == "L1" {
				if oper == "0" {
					instruction := "*SCOS,OM,"
					instruction += imei
					instruction += ",L1"
					instruction += "#"
					
					arr := util.MakeCMD(instruction)
					conn.Write(arr)

					// set "Done" as the instruction in the DB
					colQuerier := bson.M{"lockid": imei}
					changeStatus := bson.M{"$set": bson.M{"lockid": imei, "instruction": "Done"}}
					_ = c.Update(colQuerier, changeStatus)
				} else {
					// set "Fail" as the instruction in the DB
					colQuerier := bson.M{"lockid": imei}
					changeStatus := bson.M{"$set": bson.M{"lockid": imei, "instruction": "Fail"}}
					_ = c.Update(colQuerier, changeStatus)
				}
			}

			if inst == "S1" {
					// set "Done" as the instruction in the DB
					colQuerier := bson.M{"lockid": imei}
					changeStatus := bson.M{"$set": bson.M{"lockid": imei, "instruction": "Done"}}
					_ = c.Update(colQuerier, changeStatus)
			}
		}

		// Check if there are any commands from client for this imei
		if imei != "" {
			var lock common.Lock
			err = c.Find(bson.M{"lockid": imei}).One(&lock)
			if err != nil {
				log.Println("imei Not Found")
			}

			// Lock command from client
			if lock.Instruction == "lock" {
				// send lock cmd
				timestamp := util.MakeTimestamp()
				cmd := "*SCOS,OM,"
				cmd += imei
				// setting 0 as the userid tempororilly
				cmd += ",R0,1,20,0,"
				cmd += timestamp
				cmd += "#"
				arr := util.MakeCMD(cmd)
				conn.Write(arr)
			}

			// Unlock command from client
			if lock.Instruction == "unlock" {
				// send unlock cmd
				timestamp := util.MakeTimestamp()
				cmd := "*SCOS,OM,"
				cmd += imei
				// setting 0 as the userid tempororilly
				cmd += ",R0,0,20,0,"
				cmd += timestamp
				cmd += "#"
				arr := util.MakeCMD(cmd)
				conn.Write(arr)
			}

			// Reserve Beep command from client
			if lock.Instruction == "reserve" {
				// send beep cmd
				cmd := "*SCOS,OM,"
				cmd += imei
				cmd += ",V0,1#"
				arr := util.MakeCMD(cmd)
				conn.Write(arr)
				// set scooter as reserved
				cmd1 := "*SCOS,OM,"
				cmd1 += imei
				cmd1 += ",S1,10#"
				arr1 := util.MakeCMD(cmd1)
				conn.Write(arr1)
			}

			// Cancel reservation command from client
			if lock.Instruction == "cancel" {
				// send cancel cmd
				cmd1 := "*SCOS,OM,"
				cmd1 += imei
				cmd1 += ",S1,11#"
				arr1 := util.MakeCMD(cmd1)
				conn.Write(arr1)
			}

			// Find Beep command from client
			if lock.Instruction == "alarm" {
				// send beep cmd
				cmd := "*SCOS,OM,"
				cmd += imei
				cmd += ",V0,2#"
				arr := util.MakeCMD(cmd)
				conn.Write(arr)
			}
		}
	}
}

// In every 10 seconds, reguest scooter status and save information in DB
func statusUpdator(conn net.Conn) {
	defer conn.Close()

	// First, connect to DB that has all the scooter imei informations
	mongoSession, err := mgo.Dial(mongoDBHost)
	if err != nil {
		log.Println("Error Connecting MongoDBHost")
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	defer mongoSession.Close()
	c := mongoSession.DB("lime").C("lock")

	for {
		time.Sleep(10 * time.Second)
		var imei string
		// Read message from IoT
		message, _ := bufio.NewReader(conn).ReadString('\n')
			
		if strings.TrimSpace(message) != "" {
			// Parse message from IoT
			arr := strings.Split(message, ",")
			imei = arr[2]
			inst := arr[3]

			var lock common.Lock
			err = c.Find(bson.M{"lockid": imei}).One(&lock)
			if err != nil {
				log.Println("imei Not Found")
			}

			if inst == "S6" {
				power := arr[4] + "%"
				speedMode := arr[5]
				curSpeed := arr[6] + "km/h"
				chargingStatus := util.ChargingStatus(arr[7])
				bat1Volt := util.ConvertBatVoltage(arr[8])
				bat2Volt := util.ConvertBatVoltage(arr[9])

				powerInt, _ := strconv.ParseFloat(power, 64)
				if powerInt < 10 {
					// Make scooter slower
					cmd := "*SCOS,OM,"
					cmd += imei
					cmd += ",S7,1,1,1,2,2,6,6,6"
					cmd += "#"
					array := util.MakeCMD(cmd)
					conn.Write(array)
				}

				// Update lock in DB
				colQuerier := bson.M{"lockid": lock.LockID}
				changeStatus := bson.M{"$set": bson.M{"power":power, "speedmode":speedMode, "curspeed":curSpeed, "chargingstatus":chargingStatus, "bat1volt":bat1Volt, "bat2volt":bat2Volt}}
				_ = c.Update(colQuerier, changeStatus)
			}
			if inst == "D0" {
				positioning := arr[6]
				latitude := arr[7]
				longitude := arr[9]

				// Update lock in DB
				colQuerier := bson.M{"lockid": lock.LockID}
				changeStatus := bson.M{"$set": bson.M{"positioning":positioning, "latitude":latitude, "longitude":longitude}}
				_ = c.Update(colQuerier, changeStatus)
			}
			// send "Get Scooter Information" commands to connected scooter in every 10 seconds
			// send positioning cmd
			cmd := "*SCOS,OM,"
			cmd += imei
			cmd += ",D0"
			cmd += "#"
			array := util.MakeCMD(cmd)
			conn.Write(array)
			// // send get scooter information cmd
			// cmd1 := "*SCOS,OM,"
			// cmd1 += imei
			// cmd1 += ",S6"
			// cmd1 += "#"
			// arr1 := makeCMD(cmd1)
			// conn.Write(arr1)
		}
	}
}

// InitTCPServer initializes TCP server 
func InitTCPServer() {
	ln, err := net.Listen("tcp", ":8082")
	if err != nil {
		log.Fatal(err)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go handleRequestFromIoT(conn)
		go handleRequestFromClient(conn)
		go statusUpdator(conn)
	}
}
