package tcp

import (
	"bufio"
	"flag"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/digitaloceanrepo/backend/common"
	"github.com/digitaloceanrepo/backend/util"
	"github.com/google/logger"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const mongoDBHost = "127.0.0.1:27017"
const logPath = "/var/log/deezle.log"

var (
	database *mgo.Database
	verbose *bool
)

func init() {
	// Logger
	verbose = flag.Bool("verbose", false, "print info level logs to stdout")
	flag.Parse()

	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0660)
	if err != nil {
		logger.Fatalf("Failed to open log file: %v", err)
	}
	defer lf.Close()
	logger := logger.Init("Logger initialized!", *verbose, true, lf)
	defer logger.Close()

	// MongoDB session
	mongoSession, err := mgo.Dial(mongoDBHost)
	if err != nil {
		logger.Fatalf("Error Connecting MongoHost")
	}
	mongoSession.SetMode(mgo.Monotonic, true)
	database = mongoSession.DB("deezle")
}

func registerScooter(imei string) string {
	session := database.Session.Copy()
	defer session.Close()
	d := database.C("scooterstatus").With(session)

	var status common.ScooterStatus
	status.ID = imei
	err := d.Find(bson.M{"lockid": imei}).One(&status)
	_ = d.Insert(&status)

	session1 := database.Session.Copy()
	defer session1.Close()
	c := database.C("lock").With(session1)

	var lock common.Lock
	lock.LockID = imei
	lock.Locked = "true"
	lock.Reserved = "false"
	lock.Occupied = "false"
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

// handleRequestFromClient handles the commands that triggers at first from server to IoT
func handleRequestFromClient(conn net.Conn) {
	session := database.Session.Copy()
	defer session.Close()
	c := database.C("lock").With(session)
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0660)
	if err != nil {
		logger.Fatalf("Failed to open log file: %v", err)
	}
	defer lf.Close()
	logger := logger.Init("Logger initialized!", *verbose, true, lf)
	defer logger.Close()

	for {
		var imei string
		// Read message from IoT
		message, _ := bufio.NewReader(conn).ReadString('\n')
		logger.Info("Messsage from scooter", message)

		if strings.TrimSpace(message) != "" {
			// Parse message from IoT
			arr := strings.Split(message, ",")
			if len(arr) > 4 && arr[0] == "*SCOR" && arr[1] == "OM" {
				imei = arr[2]
				inst := arr[3]
				oper := arr[4]
				var key string

				// Register
				if inst == "Q0" {
					logger.Info("Connecting request from scooter:", imei)
					result := registerScooter(imei)

					if result == "Error" {
						logger.Fatalf("Error while registering scooter:", imei)
					}
					if result == "Success" {
						logger.Info("Successfully registered scooter:", imei)

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

						arr1 := util.MakeCMD(instruction)
						logger.Info("Setting(S5) default values to scooter:", imei)
						conn.Write(arr1)

						instruction = "*SCOS,OM,"
						instruction += imei
						/*
							Setting all scooters as follows
							Uploading positioning interval : 10s
						*/
						instruction += ",D1,10"
						instruction += "#"

						arr1 = util.MakeCMD(instruction)
						logger.Info("Setting(D1) default values to scooter:", imei)
						conn.Write(arr1)
					}
					if result == "Exists" {
						logger.Info("Already exists:", imei)
					}
				}

				// Heartbeat
				if inst == "H0" {
					logger.Info("Heartbeat packet from scooter:", imei)
					var lock common.Lock
					err := c.Find(bson.M{"lockid": imei}).One(&lock)
					if err != nil {
						logger.Fatalf("imei from scooter not found:", imei)
					}
					scooterStatus := arr[4]
					driveVolt := arr[5]
					networkSignal := arr[6]
					power := arr[7]
					chargingStatus := arr[8]

					lock.Power, _ = strconv.Atoi(power)
					lock.Locked = util.ScooterStatus(scooterStatus)
					lock.DriveVolt = util.ConvertVoltage(driveVolt)
					lock.NetworkSignal = networkSignal
					lock.ChargingStatus = util.ChargingStatus(chargingStatus)

					// Update lock in DB
					colQuerier := bson.M{"lockid": lock.LockID}
					changeStatus := bson.M{"$set": bson.M{"power": lock.Power, "locked": lock.Locked, "drivervolt": lock.DriveVolt, "networksignal": lock.NetworkSignal, "chargingstatus": lock.ChargingStatus}}
					_ = c.Update(colQuerier, changeStatus)
				}

				if inst == "R0" {
					logger.Info("R0 cmd from scooter:", imei)
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
					logger.Info("Sending cmd to scooter:", instruction, imei)
					conn.Write(arr)
				}

				if inst == "W0" {
					logger.Info("Alert message from scooter:", imei)
					instruction := "*SCOS,OM,"
					instruction += imei
					instruction += ",V0,2"
					instruction += "#"

					arr := util.MakeCMD(instruction)
					conn.Write(arr)
				}

				if inst == "L0" {
					logger.Info("Unlocking Response from scooter:", imei)
					instruction := "*SCOS,OM,"
					instruction += imei
					instruction += ",L0"
					instruction += "#"

					arr := util.MakeCMD(instruction)
					conn.Write(arr)

					if oper == "0" {
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
					logger.Info("Locking Response from scooter:", imei)
					instruction := "*SCOS,OM,"
					instruction += imei
					instruction += ",L1"
					instruction += "#"

					arr := util.MakeCMD(instruction)
					conn.Write(arr)

					if oper == "0" {
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

				if inst == "S6" {
					power, _ := strconv.Atoi(arr[4])
					speedMode := arr[5]
					curSpeed := arr[6] + "km/h"
					chargingStatus := util.ChargingStatus(arr[7])
					bat1Volt := util.ConvertBatVoltage(arr[8])
					bat2Volt := util.ConvertBatVoltage(arr[9])
					locked := util.ScooterStatus(arr[10])
					tmp := strings.Split(arr[11], "#")
					networkSignal := tmp[0]

					// Make scooter speed slower when battery is lower than 10%
					if power < 10 {
						logger.Info("Setting slow mode for scooter:", imei)
						// Make scooter slower
						cmd := "*SCOS,OM,"
						cmd += imei
						cmd += ",S4,1,1,1,2,2,6,6,6"
						cmd += "#"
						array := util.MakeCMD(cmd)
						conn.Write(array)
					}

					// Update lock in DB
					colQuerier := bson.M{"lockid": imei}
					changeStatus := bson.M{"$set": bson.M{"power": power, "locked": locked, "networksignal": networkSignal, "speedmode": speedMode, "curspeed": curSpeed, "chargingstatus": chargingStatus, "bat1volt": bat1Volt, "bat2volt": bat2Volt}}
					_ = c.Update(colQuerier, changeStatus)
				}
				if inst == "D0" {
					positioning := arr[6]
					latitude := util.CalculateLat(arr[7])
					longitude := util.CalculateLon(arr[9])

					// Update lock in DB
					colQuerier := bson.M{"lockid": imei}
					changeStatus := bson.M{"$set": bson.M{"positioning": positioning, "latitude": latitude, "longitude": longitude}}
					_ = c.Update(colQuerier, changeStatus)
				}
			}
		}

		// Check if there are any commands from client for this imei
		if imei != "" {
			var lock common.Lock
			err := c.Find(bson.M{"lockid": imei}).One(&lock)
			if err != nil {
				logger.Fatalf("imei from API endpoint not found:", imei)
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
				logger.Info("Sending lock request to scooter:", imei)
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
				logger.Info("Sending unlock request to scooter:", imei)
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

// InitTCPServer initializes TCP server
func InitTCPServer() {
	ln, err := net.Listen("tcp", ":8082")
	if err != nil {
		logger.Fatalf("Error creating tcp server", err)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			logger.Fatalf("Error binding client", err)
		}
		go handleRequestFromClient(conn)
	}
}
