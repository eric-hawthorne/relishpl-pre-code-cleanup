// Copyright 2012 EveryBitCounts Software Services Inc. All rights reserved.
// Use of this source code is governed by the GNU GPL v3 license, found in the LICENSE_GPL3 file.

// this package is concerned with the operation of the intermediate-code interpreter
// in the relish language.

package interp

/*
   interpreter.go - Interpreter
*/

import (
	//"os"
	"fmt"
	"relish/compiler/ast"
	"relish/compiler/token"
	. "relish/dbg"
	. "relish/runtime/data"
	"relish/rterr"
	"strconv"
	"net/url"
	"strings"
	"errors"
)

const DEFAULT_STACK_DEPTH = 50 // DEFAULT INITIAL STACK DEPTH PER THREAD

type Interpreter struct {
	rt         *RuntimeEnv
	dispatcher *dispatcher
}

func NewInterpreter(rt *RuntimeEnv) *Interpreter {
	return &Interpreter{rt: rt, dispatcher: newDispatcher(rt)}
}

func (i *Interpreter) Dispatcher() Dispatcher {
	return i.dispatcher
}

/*
Give the runtime knowledge of which artifact was originally run by the relish command.
Used to determine which web package tree to use for handling web requests.
*/
func (i *Interpreter) SetRunningArtifact(originAndArtifact string) {
	i.rt.RunningArtifact = originAndArtifact
}

/*
Runs the main method found in the specified package. 
Currently, when multimethods/methods are generated, "main" ones are prefixed by full unversioned package name, as should be zero arg methods.
*/
func (i *Interpreter) RunMain(fullUnversionedPackagePath string) {
	Logln(ANY_, " ")
	Logln(ANY_, "==============================")
	Logln(ANY_, "== RELISH Interpreter 0.0.2 ==")
	Logln(ANY_, "==============================")
	Logln(GENERATE2_, " ")
	Logln(GENERATE2_, "----------")
	Logln(GENERATE2_, "Data Types")
	Logln(GENERATE2_, "----------")
	if Logging(GENERATE2_) {
		i.rt.ListTypes()
	}
	Logln(GENERATE2_, "----------")	
	
    pkg := i.rt.Packages[fullUnversionedPackagePath]
	mm, found := pkg.MultiMethods[fullUnversionedPackagePath + "/main"]
	if !found {
		rterr.Stop("No main function defined.")
	}
	t := i.NewThread(nil)

	args := []RObject{}
	
	// TODO Figure out a way to pass command line args (or maybe just keyword ones) to the main program 
	
	method, typeTuple := i.dispatcher.GetMethod(mm, args)

	if method == nil {
		rterr.Stopf("No method '%s' is compatible with %s", mm.Name, typeTuple)
	}
	t.Push(method)
	t.Reserve(1) // For code offset pointer within method bytecode (future)

	t.Reserve(method.NumLocalVars)
	
	t.ExecutingMethod = method
	t.ExecutingPackage = pkg

	i.apply1(t, method, args)

	t.PopN(t.Pos + 1) // Pop everything off the stack for good measure.	
}



/*
Runs a service handler method. 
Assumes there is only one method for the multimethod, so a simpler dispatch that does not consider arg types is used.
Converts the arguments, whose values are all passed in as strings, into the appropriate primitive datatypes to match
the method's parameter type signature, after matching the correct input arguments to method parameters.
The parameter to argument matching process is as follows:
1. Named arguments are used as the values of the matching named method parameter.
2. Additional unmatched method parameters are filled (left to right i.e. top to bottom) from the positionalArgStringValues list.
future:
3. Additional positional and keyword arguments are assigned to variadic and kw parameters of the method, if such exist.
Matching discrepencies such as unmapped leftover arguments, or not enough arguments, cause a non-nil error return value.


Runs the method in a new stack and returns a slice of the method's return values.

TODO WE HAVE SOME SERIOUS DRY VIOLATIONS between this method and a few others in this file.
*/
func (i *Interpreter) RunServiceMethod(mm *RMultiMethod, positionalArgStringValues []string, keywordArgStringValues url.Values) (resultObjects []RObject, err error) {
	defer Un(Trace(INTERP_TR, "RunServiceMethod", fmt.Sprintf("%s", mm.Name)))	


	t := i.NewThread(nil)
	
	
	method := i.dispatcher.GetSingletonMethod(mm)

	if method == nil {
		err = fmt.Errorf("No method '%s' found.", mm.Name)
		return
	}
	
	args, err := i.matchServiceArgsToMethodParameters(method, positionalArgStringValues, keywordArgStringValues)
	if err != nil {
		return
	}



	nReturnArgs := mm.NumReturnArgs 
	
    if nReturnArgs > 0 {
	    t.Reserve(nReturnArgs)
    }
    t.Push(Int32(t.Base))  // Useless - pushing -1 here
    t.Base = t.Pos

	t.Push(method)
	t.Reserve(1) // For code offset pointer within method bytecode (future)
	
	


	// NOTE NOTE !!
	// At some point, when leaving this context, we may want to also push just above this the offset into the method's code
	// where we left off. We might wish to leave a space on the stack for that, and make initial variableOffset 3 instead of 2

	for _, arg := range args {
		t.Push(arg)
	}	
	

	t.Reserve(method.NumLocalVars)
	
	t.ExecutingMethod = method
	t.ExecutingPackage = method.Pkg

	i.apply1(t, method, args)
	
	
	t.PopN(t.Pos - t.Base + 1) 	// Leave only the return values on the stack
	
    resultObjects = t.TopN(nReturnArgs)  // Note we are hanging on to the stack array here.
	return 
}


/*
Converts the arguments, whose values are all passed in as strings, into the appropriate primitive datatypes to match
the method's parameter type signature, after matching the correct input arguments to method parameters.
The parameter to argument matching process is as follows:
1. Named arguments are used as the values of the matching named method parameter.
2. Additional unmatched method parameters are filled (left to right i.e. top to bottom) from the positionalArgStringValues list.
future:
3. Additional positional and keyword arguments are assigned to variadic and kw parameters of the method, if such exist.
Matching discrepencies such as unmapped leftover arguments, or not enough arguments, cause a non-nil error return value.

TODO - Not handling multi valued arguments, that need to be mapped to a list of strings or list of ints parameter

RMethod
	parameterNames []string               // names of parameters
	Signature      *RTypeTuple            // types of parameters
*/
func (i *Interpreter) matchServiceArgsToMethodParameters(method *RMethod, positionalArgStringValues []string, keywordArgStringValues url.Values) (args []RObject, err error) {
   
   arity := len(method.ParameterNames)
   args = make([]RObject,arity)
   paramTypes := method.Signature.Types

   var extraArgKeys []string
   for key := range keywordArgStringValues {
      foundKey := false
      for ix,paramName := range method.ParameterNames {

      	 if paramName == key {
            valStr := keywordArgStringValues.Get(key)   
            if valStr == "" {
            	panic(fmt.Sprintf("How is it that the value of argument '%s' is the empty string? Shouldn't be able to happen.", key))
            }

            // Convert string arg to an RObject, checking for type conversion errors
            err = i.setMethodArg(args, paramTypes, ix, key, valStr) 
			if err != nil {
			   return
			}            
            
            foundKey = true
            break	 	
      	 }
      }
      if ! foundKey {
      	extraArgKeys = append(extraArgKeys, key)
      }
   }

   // Finished handling keyword args. For now, throw error if extra unmapped ones. Til we can handle these with method kw params.

   if len(extraArgKeys) == 1 {
       err = fmt.Errorf("Web service request has extra argument %s.",extraArgKeys[0])
       return
   } else if len(extraArgKeys) > 1 {
       err = fmt.Errorf("Web service request has extra arguments %v.",extraArgKeys)
       return
   }

   // Now map URL path components to unfilled method parameters

   for j,valStr := range positionalArgStringValues {
   	  slotFound := false
   	  for ix,v := range args {
   	  	 if v == nil {
		      // Convert string arg to an RObject, checking for type conversion errors
		     err = i.setMethodArg(args, paramTypes, ix, valStr, valStr) 
		     if err != nil {
			     return
			 } 
             slotFound = true
             break
   	  	 }
   	  }
   	  if ! slotFound {   // Raise error if too many URL path components in web request
   	  	  nExtraArgs := len(positionalArgStringValues) - j
   	  	  if nExtraArgs == 1 {
             err = fmt.Errorf("Web service request has %d extra URI path component that doesn't map to a handler method parameter.",nExtraArgs)   
          } else {	  	  
             err = fmt.Errorf("Web service request has %d extra URI path components that don't map to handler method parameters.",nExtraArgs)
          }
          return
   	  }   
   }

   // Raise error if too few URL path components and other arguments combined to satisfy the handler method parameter signature.

   for _,v := range args {
   	  if v == nil {
   	  	  err = fmt.Errorf("Too few arguments (or URI path components) supplied to web service request.")
   	  	  return 
   	  }
   }

   return
}


/*
Converts the valStr to the type of the ith parameter of a method, and sets args[i] to the resulting RObject.
Returns type conversion errors.
argKey should be set to the valStr if the argument is not a keyword argument.
*/
func (interp *Interpreter) setMethodArg(args []RObject, paramTypes []*RType, i int, argKey string, valStr string) (err error) {
    paramType := paramTypes[i]
    switch paramType {
	case IntType : 
       var v64 int64
       v64, err = strconv.ParseInt(valStr, 0, 64)  
	   if err != nil {
		   return
	   }
	   args[i] = Int(v64)		

	case Int32Type : 
       var v64 int64
       v64, err = strconv.ParseInt(valStr, 0, 32)  
	   if err != nil {
		   return
	   }
	   args[i] = Int32(int32(v64))				

	case Int16Type : rterr.Stop("Parameter type not yet allowed in a web service handler method.")
	case Int8Type : rterr.Stop("Parameter type not yet allowed in a web service handler method.")
	case UintType : 
       var v64 uint64
       v64, err = strconv.ParseUint(valStr, 0, 64)  
	   if err != nil {
		   return
	   }
	   args[i] = Uint(v64)	

	case Uint32Type : 
       var v64 uint64
       v64, err = strconv.ParseUint(valStr, 0, 32)  
	   if err != nil {
		   return
	   }
	   args[i] = Uint32(uint32(v64))

	case Uint16Type : rterr.Stop("Parameter type not yet allowed in a web service handler method.")
	case ByteType : rterr.Stop("Parameter type not yet allowed in a web service handler method.")
	case BitType : rterr.Stop("Parameter type not yet allowed in a web service handler method.")
	case BoolType : 
       var v bool
       v, err = strconv.ParseBool(valStr)  
	   if err != nil {
		   return
	   }
	   args[i] = Bool(v)

	case FloatType : 
       var v64 float64
       v64, err = strconv.ParseFloat(valStr, 64)  
	   if err != nil {
		   return
	   }
	   args[i] = Float(v64)

	case Float32Type : rterr.Stop("Parameter type not yet allowed in a web service handler method.")
	case StringType :
       args[i] = String(valStr)

       // TODO !! IMPORTANT !! Should I be checking/filtering for injection attacks here? Do Go net libs provide help?

    default: 
       err = fmt.Errorf("Cannot map web service request argument %s to a parameter of type %s",argKey,paramType.Name)
    }
    return
}







/*
Runs the multimethod in a new stack and returns a slice of the method's return values 
*/
func (i *Interpreter) RunMultiMethod(mm *RMultiMethod, args []RObject) (resultObjects []RObject) {
	defer Un(Trace(INTERP_TR, "RunMultiMethod", fmt.Sprintf("%s", mm.Name)))	


	t := i.NewThread(nil)
	
	
	method, typeTuple := i.dispatcher.GetMethod(mm, args)

	if method == nil {
		rterr.Stopf("No method '%s' is compatible with %s", mm.Name, typeTuple)
	}
	
	
	nReturnArgs := mm.NumReturnArgs 
	
    if nReturnArgs > 0 {
	    t.Reserve(nReturnArgs)
    }
    t.Push(Int32(t.Base))  // Useless - pushing -1 here
    t.Base = t.Pos

	t.Push(method)
	t.Reserve(1) // For code offset pointer within method bytecode (future)
	
	


	// NOTE NOTE !!
	// At some point, when leaving this context, we may want to also push just above this the offset into the method's code
	// where we left off. We might wish to leave a space on the stack for that, and make initial variableOffset 3 instead of 2

	for _, arg := range args {
		t.Push(arg)
	}	
	

	t.Reserve(method.NumLocalVars)
	
	t.ExecutingMethod = method
	t.ExecutingPackage = method.Pkg

	i.apply1(t, method, args)
	
	
	t.PopN(t.Pos - t.Base + 1) 	// Leave only the return values on the stack
	
    resultObjects = t.TopN(nReturnArgs)  // Note we are hanging on to the stack array here.
	return 
}









func (i *Interpreter) EvalExpr(t *Thread, expr ast.Expr) {
	switch expr.(type) {
	case *ast.MethodCall:
		i.EvalMethodCall(t, nil, expr.(*ast.MethodCall))
	case *ast.BasicLit:
		i.EvalBasicLit(t, expr.(*ast.BasicLit))
	case *ast.Ident:
		ident := expr.(*ast.Ident)
		switch ident.Kind {
		case token.VAR:
			i.EvalVar(t, ident)
		case token.CONST:
			i.EvalConst(t, ident)
		}
	case *ast.SelectorExpr:
		i.EvalSelectorExpr(t, expr.(*ast.SelectorExpr))

	case *ast.IndexExpr:
		i.EvalIndexExpr(t, expr.(*ast.IndexExpr))

	case *ast.ListConstruction:
		i.EvalListConstruction(t, expr.(*ast.ListConstruction))		
		
	case *ast.SetConstruction:
		i.EvalSetConstruction(t, expr.(*ast.SetConstruction))
		
	case *ast.MapConstruction:
		i.EvalMapConstruction(t, expr.(*ast.MapConstruction))				
	}

}


/*
   someExpr[indexExpr]

  Evaluates someExpr to yield a collection or map. Evaluates the index exprssion to yield the index or key.
  Applies the index/key to the collection/map and leaves on the stack the value found at the index/key.

    val = list1[2]

    val = map1[! "Four"]        // return val stored under key or zero-val/nil
    val found = map1["Four"]  // return val stored under key, or zero-val/nil, and also whether key is found in the map
    found = map1[? "Four"]    // query as to whether the key is found in the map.  
*/
func (i *Interpreter) EvalIndexExpr(t *Thread, idxExpr *ast.IndexExpr) {
	defer UnM(t,TraceM(t,INTERP_TR3, "EvalIndexExpr"))

   var val RObject

   i.EvalExpr(t, idxExpr.X) // Evaluate the left part of the index expression.		      

   i.EvalExpr(t, idxExpr.Index) // Evaluate the inside-square-brackets part of the index expression.		


   obj := t.Stack[t.Pos-1]  // the object to be indexed into
   idx := t.Stack[t.Pos]    	

   collection,isCollection := obj.(RCollection)
   if ! isCollection {
		rterr.Stopf("[ ] (access by index/key) applies to a collection or map; not to an object of type %v. ", obj.Type())
   }

   if collection.IsOrdered() {
        coll := collection.(OrderedCollection)
        var ix int
        switch idx.(type) {
        case Int:
        	ix = int(int64(idx.(Int)))
        case Int32:
        	ix = int(int32(idx.(Int32)))
        case Uint:
        	ix = int(uint64(idx.(Uint)))                    	                    
        case Uint32:
        	ix = int(uint32(idx.(Uint32)))
        default:
		   rterr.Stop("Index value must be an Integer")
		}
        val = coll.At(ix)

        if val == nil {
        	val = collection.ElementType().Zero()
        }

        t.PopN(2) // Pop off the collection and its index
	    t.Push(val)        

   } else if collection.IsMap() {

   	   var found bool
   	   theMap := collection.(Map)
       val,found = theMap.Get(idx) 

        t.PopN(2) // Pop off the collection and its index

        if idxExpr.AskWhether {
           t.Push(Bool(found))
        } else {

           if val == nil {
        	  val = theMap.ValType().Zero()
           }

	       t.Push(val)  
	       if ! idxExpr.AssertExists {
        	  t.Push(Bool(found))
	       }
	    }        

   } else {
		rterr.Stopf("[ ] (access by index/key) applies to an ordered collection or a map; not to a %v. ", obj.Type())
   }
}


/*
   someExpr.ident
*/
func (i *Interpreter) EvalSelectorExpr(t *Thread, selector *ast.SelectorExpr) {
	defer UnM(t, TraceM(t, INTERP_TR3, "EvalSelectorExpr", fmt.Sprintf(".%s", selector.Sel.Name)))
	i.EvalExpr(t, selector.X) // Evaluate the left part of the selector expression.		      
	obj := t.Pop()            // the robject whose attribute value is going to be fetched.

	// To speed this up at runtime, could, during parse time, have set an attr field (tbd) of the Sel ident.
	//
	// Except... We don't even know if this name is a one-arg method or an attribute, or which setter
	// (from which type) to use. TODO TODO TODO. In this usage, in lhs of an assignment,
	// it has to be an attribute or it's an error.
	//
	
	attr, found := obj.Type().GetAttribute(selector.Sel.Name)
	if ! found {
       panic(fmt.Sprintf("Attribute or relation %s not found in type %v or supertypes.", selector.Sel.Name, obj.Type()))	
    }
	
	val, found := RT.AttrVal(obj, attr)
	if !found {
		panic(fmt.Sprintf("Object %v has no value for attribute %s", obj, selector.Sel.Name))
	}		

	t.Push(val)
}

/*
Push the value of the variable onto the top of the stack. This means the object is referred to at least twice on the 
stack. Once at the current stack top, and once at the variable's position in the stack frame.
*/
func (i *Interpreter) EvalVar(t *Thread, ident *ast.Ident) {
	defer UnM(t,TraceM(t,INTERP_TR, "EvalVar", ident.Name))
	obj, err := t.GetVar(ident.Offset)
	if err != nil {
		rterr.Stopf("Attempt to access the value of unassigned variable %s.",ident.Name)
	}
	t.Push(obj)
}

/*
Push the value of the constant with the given name onto the thread's stack.
TODO handle fully qualified constant names properly.
*/
func (i *Interpreter) EvalConst(t *Thread, id *ast.Ident) {
	defer UnM(t,TraceM(t,INTERP_TR, "EvalConst", id.Name))
	t.Push(i.rt.GetConstant(id.Name))
}

/*
TODO
BasicLit struct {
	ValuePos token.Pos   // literal position
	Kind     token.Token // token.INT, token.FLOAT, token.IMAG, token.CHAR, or token.STRING
	Value    string      // literal string; e.g. 42, 0x7f, 3.14, 1e-9, 2.4i, 'a', '\x7f', "foo" or `\m\n\o`
}
*/
func (i *Interpreter) EvalBasicLit(t *Thread, lit *ast.BasicLit) {
	defer UnM(t,TraceM(t,INTERP_TR, "EvalBasicLit", lit))
	switch lit.Kind {
	case token.INT:
		i, err := strconv.ParseInt(lit.Value, 10, 64)

		if err != nil {
			panic(err)
		}
		t.Push(Int(i))

	case token.FLOAT:
		f, err := strconv.ParseFloat(lit.Value, 64)
		if err != nil {
			panic(err)
		}
		t.Push(Float(f))
	case token.STRING:
		t.Push(String(lit.Value))
	case token.BOOL:
		b, err := strconv.ParseBool(lit.Value)
		if err != nil {
			panic(err)
		}
		t.Push(Bool(b))		
	case token.NIL:
		t.Push(NIL)				
	default:
		panic("I don't know how to interpret this kind of literal yet.")
	}
}

/*
   Evaluate a method call or type constructor call, including evaluation of the arguments. 
   Place the result(s) on the stack.

   1. Evaluate the function expression placing a method or multimethod or newly constructed object
      or newly constructed object + init multimethod
   2. If it was a type construction expression (a type spec) with no init function, stop there. The new object is
      on the thread's stack.
   3. Pop the multi-method reference and/or the newly constructed object off the stack. They'll go back on after the
      base for the new method execution is set. That is, after the creation of a new stack frame for executing the method.
   4. Determine the number of return arguments of the multi-method.
   5. PushBase - Reserve space on the stack for 
         a. the return value(s), 
         b. the base pointer which has the index of the previous base pointer. base pointer value will be -1 if stack empty
         c. the method that is to be executed.
      Note: This does not yet set the stack frame base to the new one, because we need to evaluate method call args in the
      old existing stack frame context.
   6. Place the newly constructed object if any back on the stack.
   7. Evaluate each call argument expression, pushing the resulting argument values on the stack. 
   8. If a multi-method, dispatch to find the correct RMethod. 
       (An RMethod must point to the ast.MethodDeclaration which has the code body.) 
       The correct method is determined by multimethod dispatch on the runtime types of the required positional arguments.
   9. Set t.Base to the new base pointer's stack index, thus switching context to the new stack frame.
   10. Apply the method to the arguments. (If a relish method, the method code uses the stack to find the arguments. If is
       a builtin method implemented in go, uses a slice of RObjects which is a slice containing the args at the top of the stack)
   11. Result value(s) are placed just below the current base pointer, during execution in the method relish code of assignments
       to return arg variables, or alternately upon the execution of a relish "=>" expr1 expr2 (return) statement. 
   12. PopBase - set t.Base to the value stored in the current base pointer. Set t.Pos to the index of the last return val. 


   working data for statement, expr evaluation during method execution            <-- t.Pos
   working data for statement, expr evaluation during method execution
   paramOrLocalVar2
   paramOrLocalVar1 
   paramOrLocalVar0
   [reserve for code position in method body bytecode? RESERVED BUT NOT USED]
   method
   base              ---       . . . . . . . . . . . . . . . . . . . . . . . .  . <-- t.Base
   retval2              |
   retval1              |
   working data         | 
   working data         |
   paramOrLocalVar2     |    
   paramOrLocalVar1     |
   paramOrLocalVar0     |
   [reserve for code position in method body bytecode? RESERVED BUT NOT USED]
   method               |
   base      --       <-
   retval1     |
               v


   Note. If Thread t2 is supplied non-nil, it refers to a newly spawned thread of which t is the parent.
   In this case, the method call arguments (and the expression that yields the multimethod) will be evaluated 
   in the parent thread t and its stack, then the stack frame of the call will be copied to t2's stack, 
   and the method application to the arguments will be performed in the new thread (go-routine actually) using t2's stack.


TODO Put nArgs in variants of this method !!!!!!!!!!!!

*/
func (i *Interpreter) EvalMethodCall(t *Thread, t2 *Thread, call *ast.MethodCall) (nReturnArgs int) {
	defer UnM(t,TraceM(t,INTERP_TR, "EvalMethodCall"))

    // Evaluate the function expression - function name, lambda (TBD), or type name
    // and put the method or multimethod on the stack,
    // TODO or, if it is a type constructor, should put the type constructor function on the
    // stack. This is TBD.

	isTypeConstructor, hasInitFunction := i.EvalFunExpr(t, call.Fun) // token.FUN (regular method) or token.TYPE (constructor)
	
	

	// This still doesn't handle implicit constructors that have keyword args corresponding to attributes of the
	// type ad supertypes. !!!! TODO
	//
	
	var newObject RObject
	var meth RObject
	
	if isTypeConstructor {
		if ! hasInitFunction {
			nReturnArgs = 1
			return 
		}
	    meth = t.Pop() // put it back after the base pointer.	
	    newObject = t.Pop()	
	} else {
	    meth = t.Pop() // put it back after the base pointer.			
	}
	
    // TODO Question - Why are we pushing the method and popping it again like this,
    // Why not just have EvalFunExpr return the method or multimethod as an RObject? breaks Eval.. method conventions, but more efficient.


	LoglnM(t,INTERP_TR, meth)
	switch meth.(type) {
	case *RMultiMethod:
		nReturnArgs = meth.(*RMultiMethod).NumReturnArgs
	case *RMethod:
		nReturnArgs = meth.(*RMethod).NumReturnArgs
	default:
		panic("Expecting a Method or MultiMethod.")
	}

    // Now we know how many return argument slots to reserve on stack below the base pointer...

	newBase := t.PushBase(nReturnArgs) // begin but don't complete, storing outer routine context. 

	// NOTE NOTE !!
	// At some point, when leaving this context, we may want to also push just above this the offset into the method's code
	// where we left off. We might wish to leave a space on the stack for that, and make initial variableOffset 3 instead of 2

    constructorArg := 0
    if isTypeConstructor {
	   t.Push(newObject)
	   constructorArg = 1
    }  

    p0 := t.Pos
	for _, expr := range call.Args {
		i.EvalExpr(t, expr)
	}
	p1 := t.Pos
	nArgs := p1 - p0
	
	// 
	// TODO We are going to have to handle varargs differently here. Basically, eval and push only the non-variable
	// args here, then, below, reserve space for one list (of varargs) then reserve space for the local vars, 
	// and finally, just before apply1, eval and push the extra args onto the stack, then remove them into the list.
	// Or do we just use the stack itself as the list of extra args?s???????? TODO !!!!!
	//

	t.SetBase(newBase) // Now we're in the context of the newly called function.

	// Put this back here!!!
	//defer t.PopBase() // We need to worry about panics leaving the stack state inconsistent. TODO

	var method *RMethod
	var typeTuple *RTypeTuple
	switch meth.(type) {
	case *RMultiMethod:
		mm := meth.(*RMultiMethod)
		
// !!!!!!!!!!!!!!! TODO TODO TODO SEVERELY WRONG !!! Can't do dispatch at all
// until have evaluated the argument expressions and know how many args were pushed on
// the stack.	
		
		method, typeTuple = i.dispatcher.GetMethod(mm, t.TopN(constructorArg + nArgs)) // nArgs is WRONG! Use Type.Param except vararg
		if method == nil {
			if isTypeConstructor && nArgs == 0 {  // There is no other-argless init<TypeName> method.
               // Unsetup the aborted constructor method call.
	           // TODO This is really inefficient!! Do something else for simple no-method constructions.
	           t.PopBase() // We need to worry about relish-level panics leaving the stack state inconsistent. TODO   
	           t.PopN(nReturnArgs) // Get rid of space reserved for return args of aborted constructor method call.     
               t.Push(newObject)  // Again, this time under the base of the alleged but no found constructor.
	           nReturnArgs = 1     
               return                       // Just return the uninitialized object.
            }

            // This is actually a no-compatible method found dynamic-dispatch error (i.e. a runtime-detected type compatibility error).
			//
			rterr.Stopf("No method '%s' visible from within %s is compatible with %s", mm.Name, t.ExecutingPackage.Name,typeTuple)
		}
		LoglnM(t,INTERP_, "Multi-method dispatched to ", method)
	case *RMethod:
		method = meth.(*RMethod)
	default:
		panic("Expecting a Method or MultiMethod.")
	}

    if t2 == nil { 

		// put currently executing method on stack in reserved parking place
		t.Stack[newBase+1] = method

		t.ExecutingMethod = method       // Shortcut for dispatch efficiency
		t.ExecutingPackage = method.Pkg  // Shortcut for dispatch efficiency

		t.Reserve(method.NumLocalVars) // This only works with next TopN call because builtin methods have 0 local vars.

		i.apply1(t, method, t.TopN(constructorArg + nArgs)) // Puts results on stack BELOW the current stack frame.	

    } else { // This is a go statement execution
    	t2.copyStackFrameFrom(t, nReturnArgs)  // copies the frame for the execution of this method, 
    	                                               // including space reserved for return val(s)
        go i.GoApply(t2, method)
    }

	t.PopBase() // We need to worry about relish-level panics leaving the stack state inconsistent. TODO

	return
}

/*
Assumes that the base pointer for executing the new method has been set in thread t,
but that the method has not been pushed onto t's stack nor has the space for the method's local vars been reserved on t's stack.

Applies the method using thread t to pre-evaluated arguments that are in the current frame of t's stack.
*/
func (i *Interpreter) GoApply(t *Thread, method *RMethod) {
    
	t.Stack[t.Base+1] = method

	t.ExecutingMethod = method       // Shortcut for dispatch efficiency
	t.ExecutingPackage = method.Pkg  // Shortcut for dispatch efficiency

    nArgs := t.Pos - t.Base - 3
	t.Reserve(method.NumLocalVars)   // This only works with next TopN call because builtin methods have 0 local vars.
 

	i.apply1(t, method, t.TopN(nArgs)) // Puts results on stack BELOW the current stack frame.	

	t.PopBase()
	t.PopN(method.NumReturnArgs) // Pop everything off the stack for good measure.		
}


func (i *Interpreter) CreateList(elementType *ast.TypeSpec) (List, error) {
	// Find the type
   typ, typFound := i.rt.Types[elementType.Name.Name]
   if ! typFound {
      rterr.Stopf("List Element Type '%s' not found.",elementType.Name.Name)	
   }

   // TODO sorting-lists
   return i.rt.Newrlist(typ, 0, -1, nil, nil)
}

/*
Not handling sorting sets yet.
*/
func (i *Interpreter) CreateSet(elementType *ast.TypeSpec) (RCollection, error) {
	// Find the type
   typ, typFound := i.rt.Types[elementType.Name.Name]
   if ! typFound {
      rterr.Stopf("Set Element Type '%s' not found.",elementType.Name.Name)	
   }

   // TODO sorting-sets
   return i.rt.Newrset(typ, 0, -1, nil)
}

/*
Note it is not really the key type, since the type of the keyType typeSpec has a collectionTypeSpec
*/
func (i *Interpreter) CreateMap(keyType *ast.TypeSpec, valType *ast.TypeSpec) (Map, error) {
	// Find the key and value types
	
   keyTyp, typFound := i.rt.Types[keyType.Name.Name]
   if ! typFound {
      rterr.Stopf("Map Key Type '%s' not found.",keyType.Name.Name)	
   }	
   valTyp, typFound := i.rt.Types[valType.Name.Name]
   if ! typFound {
      rterr.Stopf("Map Value Type '%s' not found.",valType.Name.Name)	
   }

   // TODO sorting-maps
   return i.rt.Newmap(keyTyp, valTyp, 0, -1, nil, nil)
}



/*
Creates a list, and populates it from explicit element expressions or by executing a SQL query in the database.
Leaves the constructed and possibly populated list as the top of the stack.

// EGH A ListConstruction node represents a list constructor invocation, which may be a list literal, a new empty list of a type, or
// a list with a db sql query where clause specified as the source of list members.

ListConstruction struct {
    Type *TypeSpec     // Includes the CollectionTypeSpec which must be a spec of a List.
	Elements  []Expr    // explicitly listed elements; or nil        
	Query     Expr     // must be an expression evaluating to a String containing a SQL WHERE clause (without the "WHERE"), or nil
	                   // Note eventually it should be more like OQL where you can say e.g. engine.horsePower > 120 when fetching []Car
}
*/
func (i *Interpreter) EvalListConstruction(t *Thread, listConstruction *ast.ListConstruction) {
	defer UnM(t,TraceM(t,INTERP_TR, "EvalListConstruction"))

    list, err := i.CreateList(listConstruction.Type)
    if err != nil {
	   panic(err)
    }
    t.Push(list)

   nElem := len(listConstruction.Elements)
   if nElem > 0 {
		for _, expr := range listConstruction.Elements {
			i.EvalExpr(t, expr)
		}	
		
       err = i.rt.ExtendCollectionTypeChecked(list, t.TopN(nElem), t.EvalContext) 	
       if err != nil {
	      rterr.Stop(err)
       }		

       t.PopN(nElem)
	
   } else if listConstruction.Query != nil { // Database select query to fill the list
	  // TODO
	
	
	  // TODO Why can't we do this query syntax transformation at generation time, so we only do it
	  // once per occurrence in the program text, as long as it is a literal string.
	  // !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
	  //
			
	  i.EvalExpr(t, listConstruction.Query)
	
	  qExpr := t.Pop()	
	
	  qS,isString := qExpr.(String)
	  if ! isString {
	     rterr.Stop("Query expression used in list construction must evaluate to a String.")	
	  }
	  query := string(qS)
	  radius := 1
	  if strings.HasPrefix(query, "lazy: ") {
		 query = query[6:]
	     radius = 0	 
	  }

	
	  objs := []RObject{} // TODO Use the existing List's RVector somehow
	
	  // objs := list.Vector().(*[]RObject)
	
//	  rv := list.Vector()
//      objs := (*[]RObject)(rv)


	

      err = i.rt.DB().FetchN(list.ElementType(), query, radius, &objs)		
      if err != nil {
	      rterr.Stop(err)
      }	

      list.ReplaceContents(objs)

      // fmt.Println(len(*objs))

   } else if listConstruction.Generator != nil { // Generator expression to yield elements  
      i.iterateGenerator(t, listConstruction.Generator, 1)
      list.ReplaceContents(t.Objs)
      t.Objs = nil
   }
   return
}

/*
   Given a for-range statement which is of the special type that constitutes a generator expression,
   causes the iteration of the range statement to produce all of the generated results.
   nResultsPerIteration is the expected number of result values expected to be yielded per iteration
   of the for loop. A runtime error will occur if the for-range statement does not yield this number
   of results per iteration.
   Results are collected in a slice of RObjects. If there is more than one result per iteration,
   the result values are interleaved in the returned slice as follows iteration1result1 i1r2 i2r1 i2r2 i3r1 i3r2 
*/
func (i *Interpreter) iterateGenerator(t *Thread, rangeStmt *ast.RangeStatement, nResultsPerIteration int) {
	t.YieldCardinality = nResultsPerIteration
	i.ExecForRangeStatement(t, rangeStmt)
}


/*
Creates a set, and populates it from explicit element expressions or by executing a SQL query in the database.
Leaves the constructed and possibly populated set as the top of the stack.

// EGH A SetConstruction node represents a set constructor invocation, which may be a set literal, a new empty set of a type, or
// a set with a db sql query where clause specified as the source of set members.

SetConstruction struct {
    Type *TypeSpec     // Includes the CollectionTypeSpec which must be a spec of a Set.
	Elements  []Expr    // explicitly listed elements; or nil        
	Query     Expr     // must be an expression evaluating to a String containing a SQL WHERE clause (without the "WHERE"), or nil
	                   // Note eventually it should be more like OQL where you can say e.g. engine.horsePower > 120 when fetching []Car
}
*/
func (i *Interpreter) EvalSetConstruction(t *Thread, setConstruction *ast.SetConstruction) {
	defer UnM(t,TraceM(t,INTERP_TR, "EvalSetConstruction"))

    set, err := i.CreateSet(setConstruction.Type)
    if err != nil {
	   panic(err)
    }
    t.Push(set)

   nElem := len(setConstruction.Elements)
   if nElem > 0 {
		for _, expr := range setConstruction.Elements {
			i.EvalExpr(t, expr)
		}	
		
       err = i.rt.ExtendCollectionTypeChecked(set, t.TopN(nElem), t.EvalContext) 	
       if err != nil {
	      rterr.Stop(err)
       }		

       t.PopN(nElem)
	
   } else if setConstruction.Query != nil { // Database select query to fill the list
	  // TODO
	
	
	  // TODO Why can't we do this query syntax transformation at generation time, so we only do it
	  // once per occurrence in the program text, as long as it is a literal string.
	  // !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
	  //
			
	  i.EvalExpr(t, setConstruction.Query)
	
	  qExpr := t.Pop()	
	
	  qS,isString := qExpr.(String)
	  if ! isString {
	     rterr.Stop("Query expression used in set construction must evaluate to a String.")	
	  }
	  query := string(qS)
	  radius := 1
	  if strings.HasPrefix(query, "lazy: ") {
		 query = query[6:]
	     radius = 0	 
	  }

	
	  objs := []RObject{} // TODO Use the existing List's RVector somehow
	
	  // objs := list.Vector().(*[]RObject)
	
//	  rv := list.Vector()
//      objs := (*[]RObject)(rv)


	

      err = i.rt.DB().FetchN(set.ElementType(), query, radius, &objs)		
      if err != nil {
	      rterr.Stop(err)
      }	

      aSet := set.(AddableCollection)
      for _,obj := range objs {
		 aSet.Add(obj, t.EvalContext) 
      }


      // fmt.Println(len(*objs))

   } else if setConstruction.Generator != nil { // Generator expression to yield elements  
      i.iterateGenerator(t, setConstruction.Generator, 1)
      aSet := set.(AddableCollection)
      for _,obj := range t.Objs {
		 aSet.Add(obj, t.EvalContext) 
      }
      t.Objs = nil
   }
	return
}



/*
Creates a map, and populates it from explicit entry expressions. 
Leaves the constructed and possibly populated map as the top of the stack.

MapConstruction struct {
    Type *TypeSpec     // Includes the CollectionTypeSpec which must be a spec of a Map.
    ValType *TypeSpec     // Type of the values
    Keys []Expr         // explicitly listed keys; or nil
	Elements  []Expr    // explicitly listed elements; or nil        
}

*/
func (i *Interpreter) EvalMapConstruction(t *Thread, mapConstruction *ast.MapConstruction) {
	defer UnM(t,TraceM(t,INTERP_TR, "EvalMapConstruction"))

    theMap, err := i.CreateMap(mapConstruction.Type, mapConstruction.ValType)
    if err != nil {
	   panic(err)
    }
    t.Push(theMap)

   nElem := len(mapConstruction.Elements)
   if nElem > 0 {
		for j, valExpr := range mapConstruction.Elements {
			i.EvalExpr(t, mapConstruction.Keys[j])
			i.EvalExpr(t, valExpr)
		}	
		
       err = i.rt.ExtendMapTypeChecked(theMap, t.TopN(nElem *2), t.EvalContext) 	
       if err != nil {
	      rterr.Stop(err)
       }		

       t.PopN(nElem * 2)
	} else if mapConstruction.Generator != nil { // Generator expression to yield elements  
      i.iterateGenerator(t, mapConstruction.Generator, 2)
      n := len(t.Objs)
      for k := 0; k < n; k+=2 {
         key := t.Objs[k]
         val := t.Objs[k+1] 
		 theMap.Put(key, val, t.EvalContext) 
      }
      t.Objs = nil
   }
	
 
	return
}






///// From here to next ///// is special purpose code
/*
Implements interface MethodEvaluationContext defined in package relish/runtime/data
*/
type methodEvaluationContext struct {
	interpreter *Interpreter
	thread      *Thread
}

/*
Evaluates a single-valued function after dispatching to find the correct function implementation.
*/
func (context *methodEvaluationContext) EvalMultiMethodCall(mm *RMultiMethod, args []RObject) RObject {
	return context.interpreter.evalMultiMethodCall1ReturnVal(context.thread, mm, args)
}

/*
Special-purpose variant of method evaluation, used as part of implementation of MethodEvaluationContext interface.
Evaluates a single-valued multi-method on some pre-evaluated arguments, and returns the result. 

Used for example to evaluate collection-sort comparison functions on collection members.
*/
func (i *Interpreter) evalMultiMethodCall1ReturnVal(t *Thread, mm *RMultiMethod, args []RObject) RObject {
	defer UnM(t,TraceM(t,INTERP_TR, "evalMultiMethodCall"))
	LoglnM(t,INTERP_TR, mm)

	//   t.Push(multiMethod)

	//   meth := t.Pop() // put it back after the base pointer.

	nReturnArgs := mm.NumReturnArgs // Assuming this is 1   !!!

	newBase := t.PushBase(nReturnArgs) // begin but don't complete, storing outer routine context. 

	// NOTE NOTE !!
	// At some point, when leaving this context, we may want to also push just above this the offset into the method's code
	// where we left off. We might wish to leave a space on the stack for that, and make initial variableOffset 3 instead of 2

	for _, arg := range args {
		t.Push(arg)
	}
	// 
	// TODO We are going to have to handle varargs differently here. Basically, eval and push only the non-variable
	// args here, then, below, reserve space for one list (of varargs) then reserve space for the local vars, 
	// and finally, just before apply1, eval and push the extra args onto the stack, then remove them into the list.
	// Or do we jusy use the list on the stack???????? TODO !!!!!
	//

	t.SetBase(newBase) // Now we're in the context of the newly called function.

	// Put this back here!!!
	//defer t.PopBase() // We need to worry about panics leaving the stack state inconsistent. TODO

	var method *RMethod
	var typeTuple *RTypeTuple

	method, typeTuple = i.dispatcher.GetMethod(mm, args) // len call.Args is WRONG! Use Type.Param except vararg
	if method == nil {
		rterr.Stopf("No method '%s' is compatible with %s", mm.Name, typeTuple)
	}
	LoglnM(t,INTERP_, "Multi-method dispatched to ", method)

	// put currently executing method on stack in reserved parking place
	t.Stack[newBase+1] = method

	t.ExecutingMethod = method       // Shortcut for dispatch efficiency
	t.ExecutingPackage = method.Pkg  // Shortcut for dispatch efficiency	

	t.Reserve(method.NumLocalVars)

	i.apply1(t, method, args) // Puts results on stack BELOW the current stack frame.	

	t.PopBase() // We need to worry about panics leaving the stack state inconsistent. TODO

	return t.Pop() // Assuming single valued function!
}

///// From here up to previous ///// is special purpose code

/*
Evaluate the expression which must end up as either a RMultiMethod or an RMethod or a Type. 
If a RMultiMethod or RMethod, put that on the stack.
If a Type, then if TODO!!!!!!!!!!!!

If this is a type constructor call, then return isTypeConstructor = true
and place the newly allocated but uninitialized object on the stack.
If additionally an init<TypeName> function was found, set the hasInitFunc = true
and place the init<TypeName> multimethod on the stack.
If it is not a constructor call, place the found multimethod on the stack. 
*/
func (i *Interpreter) EvalFunExpr(t *Thread, fun ast.Expr) (isTypeConstructor bool, hasInitFunc bool) {
	defer UnM(t,TraceM(t,INTERP_TR2, "EvalFunExpr"))
	var methodKind token.Token
	switch fun.(type) {
	case *ast.Ident:
		id := fun.(*ast.Ident)
		methodKind = id.Kind
		switch methodKind {
		case token.FUNC:
			multiMethod, found := t.ExecutingPackage.MultiMethods[id.Name]
			if !found {
				rterr.Stopf("No method named '%s' is visible from within package %s.", id.Name, t.ExecutingPackage.Name)
			}
			t.Push(multiMethod)
			
		case token.TYPE:
			var obj RObject
			var err error
			obj, err = i.rt.NewObject(id.Name)
			if err != nil {
				panic(err)
			}
			t.Push(obj)
			
			isTypeConstructor = true			
			
			slashPos := strings.LastIndex(id.Name,"/")
			var typeName string
			var initMethodName string
			if slashPos >= 0 {
				typeName = id.Name[slashPos + 1:]
				initMethodName = id.Name[:slashPos+1] + "init" + typeName
			} else {
				typeName = id.Name
				initMethodName = "init" + typeName
			}
			
			multiMethod, found := t.ExecutingPackage.MultiMethods[initMethodName]
			if found {
			   t.Push(multiMethod)	
			   hasInitFunc = true	
			}	
			
			
		default:
			panic("Wrong type of ident for function name.")
		}
	default:
		panic("I don't handle lambda expressions yet!")
	}
	return 
}

/*
DEPRECATED
Apply the appropriate method implementation to the arguments, after determining the appropriate method by
multi-argument polymorphic dispatch. 
Puts the results on the stack, in reserved slots BELOW the current method-call's stack frame.
TODO.
*/
func (i *Interpreter) Apply(t *Thread, mm *RMultiMethod, args []RObject) {
	method, typeTuple := i.dispatcher.GetMethod(mm, args)
	if method == nil {
		rterr.Stopf("No method '%s' is compatible with %s", mm.Name, typeTuple)
	}
	i.apply1(t, method, args)
}

/*
Apply the method implementation to the arguments.
Puts the results on the stack, in reserved slots BELOW the current method-call's stack frame.
Does not pop the m method's stack frame from the stack i.e. does not pop (move down) the base pointer.

TODO TODO We cannot have the return values on the stack in reverse order like this.
It will not work for using the values as args to the next outer method.
*/
func (i *Interpreter) apply1(t *Thread, m *RMethod, args []RObject) {
	defer UnM(t, TraceM(t,INTERP_TR, "apply1", m, "to", args))	
//	if strings.Contains(m.String(),"spew") {
//		fmt.Println(args)
//	} 

	if Logging(STACK_) {
		t.Dump()
	}
	if m.PrimitiveCode == nil {
		if m.ReturnArgsNamed {
			n := m.NumReturnArgs
            for j,typ := range m.ReturnSignature.Types {
            	t.Stack[t.Base+j-n] = typ.Zero()
            }
		}
		i.ExecBlock(t, m.Code.Body)
		// Now maybe type-check the return values !!!!!!!! This is really expensive !!!!
	} else {
		objs := m.PrimitiveCode(args)
		n := len(objs)
		for j, obj := range objs {
			t.Stack[t.Base+j-n] = obj   // was t.Base-j-1 (return args in reverse order on stack)
		}
	}
}

/*
Evaluate a block statement. Any return values will have been placed into the appropriate return-value stack
slots. Returns whether the next outermost loop should be broken or continued, or whether the containing methods should be
returned from.
*/
func (i *Interpreter) ExecBlock(t *Thread, b *ast.BlockStatement) (breakLoop, continueLoop, returnFrom bool) {
	defer UnM(t,TraceM(t,INTERP_TR2, "ExecBlock"))
	for _, statement := range b.List {
		breakLoop, continueLoop, returnFrom := i.ExecStatement(t, statement)
		if breakLoop || continueLoop || returnFrom {
			break
		}
	}
	return
}

func (i *Interpreter) ExecStatement(t *Thread, stmt ast.Stmt) (breakLoop, continueLoop, returnFrom bool) {
	defer UnM(t,TraceM(t,INTERP_TR3, "ExecStatement"))
	switch stmt.(type) {
	case *ast.IfStatement:
		breakLoop, continueLoop, returnFrom = i.ExecIfStatement(t, stmt.(*ast.IfStatement))
	case *ast.WhileStatement:
		breakLoop, continueLoop, returnFrom = i.ExecWhileStatement(t, stmt.(*ast.WhileStatement))
	case *ast.RangeStatement:
		breakLoop, continueLoop, returnFrom = i.ExecForRangeStatement(t, stmt.(*ast.RangeStatement))
	case *ast.MethodCall:
		i.ExecMethodCall(t, nil, stmt.(*ast.MethodCall))
	case *ast.AssignmentStatement:
		i.ExecAssignmentStatement(t, stmt.(*ast.AssignmentStatement))
	case *ast.ReturnStatement:		
		returnFrom = i.ExecReturnStatement(t, stmt.(*ast.ReturnStatement)) 
	case *ast.BlockStatement:
		breakLoop, continueLoop, returnFrom = i.ExecBlock(t, stmt.(*ast.BlockStatement))
	case *ast.GoStatement:
		i.ExecGoStatement(t, stmt.(*ast.GoStatement))		
//	case *ast.DeferStatement:
//		breakLoop, continueLoop, returnFrom = i.ExecDeferStatement(t, stmt.(*ast.DeferStatement))		
		
	default:
		panic("I don't know how to handle this kind of statement.")
	}
	return
}

/*
 */
func (i *Interpreter) ExecIfStatement(t *Thread, stmt *ast.IfStatement) (breakLoop, continueLoop, returnFrom bool) {
	defer UnM(t, TraceM(t,INTERP_TR2, "ExecIfStatement"))
	i.EvalExpr(t, stmt.Cond)
	if t.Pop().IsZero() {
		if stmt.Else != nil {
			breakLoop, continueLoop, returnFrom = i.ExecStatement(t, stmt.Else)
		}
	} else {
		breakLoop, continueLoop, returnFrom = i.ExecBlock(t, stmt.Body)
	}
	return
}

/*
   Execute the MethodCall in a new go thread, with a new Relish stack. (A "Thread" is an object representing a relish stack)
*/
func (i *Interpreter) ExecGoStatement(parent *Thread, stmt *ast.GoStatement) {
	t := i.NewThread(parent)
	i.ExecMethodCall(parent, t, stmt.Call)	
}

func (i *Interpreter) ExecWhileStatement(t *Thread, stmt *ast.WhileStatement) (breakLoop, continueLoop, returnFrom bool) {
	defer UnM(t,TraceM(t,INTERP_TR2, "ExecWhileStatement"))
	i.EvalExpr(t, stmt.Cond)
	if t.Pop().IsZero() {
		if stmt.Else != nil {
			breakLoop, continueLoop, returnFrom = i.ExecStatement(t, stmt.Else)
		}
	} else {
		breakLoop, _, returnFrom = i.ExecBlock(t, stmt.Body)
		for (!breakLoop) && (!returnFrom) {
			i.EvalExpr(t, stmt.Cond)
			if t.Pop().IsZero() {
				break
			}
			breakLoop, _, returnFrom = i.ExecBlock(t, stmt.Body)
		}
		breakLoop = false
		continueLoop = false
	}
	return
}

/* ast.RangeStatement
For        token.Pos   // position of "for" keyword
KeyAndValues   []Expr           // Any of Key or Values may be nil, though all should not be.

X          []Expr        // collectionsto range over are the values of these exprs. Need to handle multiple expressions.
Body       *BlockStatement
*/

func (i *Interpreter) ExecForRangeStatement(t *Thread, stmt *ast.RangeStatement) (breakLoop, continueLoop, returnFrom bool) {
	defer UnM(t,TraceM(t,INTERP_TR2, "ExecForRangeStatement"))

	kvLen := len(stmt.KeyAndValues)

	stackPosBefore := t.Pos

	// evaluate the collection expression(s) and push it (them) on the stack.

	// Collection 0 is first one pushed on stack. Others up from there.
	for _, expr := range stmt.X {
		i.EvalExpr(t, expr)
	}
	lastCollectionPos := t.Pos
	nCollections := lastCollectionPos - stackPosBefore // the number of collections pushed onto the stack.	

	var iters []<-chan RObject
	for collPos := stackPosBefore + 1; collPos <= lastCollectionPos; collPos++ {
		collection := t.Stack[collPos].(RCollection)
		iter := collection.Iter()
		iters = append(iters, iter)
	}

	var idx int64 = 0 // value of index integer in each loop iteration

	keyOffset := kvLen - len(iters) // number of index or key vars before the first value var in for statement

	// TODO Move the decision on what kind this is to here!
	// Based on num and type of collections and lvars

	// Here are the different varieties this could be:
	// 1. for i key val in orderedMap  // keyOffset == 2, 1 coll, coll[0] is map
	// 2. for key val in mapOrOrderedMap // keyOffset == 1, 1 coll, coll[0] is map
	// 3. for i val in listOrOrderedSet // keyOffset == 1, 1 coll, coll[0] is listOrOrderedSet
	// 4. for i val1 val2 in listOrOrderedSetOrOrderedMap listOrOrderedSetOrOrderedMap  // if map is keys
	// 5. for val in anyCollection  // if map then is keys	
	// 6. for val1 val2 in listOfOrderedSetOrOrderedMap listOrOrderedSetOrOrderedMap  // if map is keys

	collPos := stackPosBefore + 1
	collection := t.Stack[collPos].(RCollection)

	switch keyOffset {
	case 2:

		// 1. for i key val in orderedMap  // keyOffset == 2, 1 coll, coll[0] is map		

		if nCollections != 1 {
			rterr.Stop("Expecting only one collection, (an ordered map), when there are two more vars than collections.")
		}
		if !collection.IsMap() {
			rterr.Stop("Expecting an ordered map, when construct is 'for i key val in orderedMap'.")
		}
		if !collection.IsOrdered() {
			rterr.Stop("Expecting an ordered map, when construct is 'for i key val in orderedMap'.")
		}

		// now do the looping

		for {
			moreMembers := false

			key, nextMemberFound := <-iters[0]

			if nextMemberFound {
				moreMembers = true
			}

			if !moreMembers {
				break
			}

			// Assign to the index variable

			idxVar := stmt.KeyAndValues[0].(*ast.Ident)
			LogM(t, INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, idxVar.Name, idxVar.Offset)
			t.Stack[t.Base+idxVar.Offset] = Int(idx)

			// Assign to the key variable

			keyVar := stmt.KeyAndValues[1].(*ast.Ident)
			LogM(t, INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, keyVar.Name, keyVar.Offset)
			t.Stack[t.Base+keyVar.Offset] = key

			// Fetch the map value for the key 	

			mapColl := collection.(Map)
			obj, _ := mapColl.Get(key) // TODO Implement Get!!!!!!!!!!!!	

			// Assign to the value variable

			valVar := stmt.KeyAndValues[2].(*ast.Ident)
			LogM(t, INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, valVar.Name, valVar.Offset)
			t.Stack[t.Base+valVar.Offset] = obj

			// Execute loop body	

			breakLoop, _, returnFrom = i.ExecBlock(t, stmt.Body)

			if breakLoop || returnFrom {
				breakLoop = false
				continueLoop = false
				break
			}

			// increment the loop iteration index
			idx += 1

		}
	case 1:

		if nCollections == 1 {
			if collection.IsMap() {

				// 2. for key val in mapOrOrderedMap 
				
				theMap := collection.(Map)
				
				for {
					moreMembers := false

					key, nextMemberFound := <-iters[0]

					if nextMemberFound {
						moreMembers = true
					}

					if !moreMembers {
						break
					}

					// Assign to the key variable

					keyVar := stmt.KeyAndValues[0].(*ast.Ident)
					LogM(t, INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, keyVar.Name, keyVar.Offset)
					t.Stack[t.Base+keyVar.Offset] = key

					// Assign to the value variable

					valVar := stmt.KeyAndValues[1].(*ast.Ident)
					LogM(t, INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, valVar.Name, valVar.Offset)
					t.Stack[t.Base+valVar.Offset],_ = theMap.Get(key)

					// Execute loop body	

					breakLoop, _, returnFrom = i.ExecBlock(t, stmt.Body)

					if breakLoop || returnFrom {
						breakLoop = false
						continueLoop = false
						break
					}
				}				

			} else {

				// 3. for i val in listOrOrderedSet 

				if !collection.IsOrdered() {
					rterr.Stop("Expecting a list or ordered set when construct is 'for i val in listOrOrderedSet'.")
				}

				// now do the looping

				for {
					moreMembers := false

					obj, nextMemberFound := <-iters[0]

					if nextMemberFound {
						moreMembers = true
					}

					if !moreMembers {
						break
					}

					// Assign to the index variable

					idxVar := stmt.KeyAndValues[0].(*ast.Ident)
					LogM(t, INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, idxVar.Name, idxVar.Offset)
					t.Stack[t.Base+idxVar.Offset] = Int(idx)

					// Assign to the value variable

					valVar := stmt.KeyAndValues[1].(*ast.Ident)
					LogM(t, INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, valVar.Name, valVar.Offset)
					t.Stack[t.Base+valVar.Offset] = obj

					// Execute loop body	

					breakLoop, _, returnFrom = i.ExecBlock(t, stmt.Body)

					if breakLoop || returnFrom {
						breakLoop = false
						continueLoop = false
						break
					}

					// increment the loop iteration index
					idx += 1
				}
			}
		} else { // more than one collection

			// 4. for i val1 val2 in listOrOrderedSetOrOrderedMap listOrOrderedSetOrOrderedMap  // if map, is keys

			for collPos = stackPosBefore + 1; collPos <= lastCollectionPos; collPos++ {
				collection := t.Stack[collPos].(RCollection)
				if !collection.IsOrdered() {
					rterr.Stop("Expecting lists or other ordered collections when construct is 'for i val1 val2 ... in coll1 coll2 ...'.")
				}
			}

			// now do the looping

			for {
				moreMembers := false

				for k := 0; k < len(iters); k++ {
					obj, nextMemberFound := <-iters[k]

					if nextMemberFound {
						moreMembers = true
					} else if (k == len(iters)-1) && (!moreMembers) { // we are done. All iterators are exhausted.
						break
					}

					// Assign to the value variable

					valVar := stmt.KeyAndValues[k+1].(*ast.Ident)
					LogM(t,INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, valVar.Name, valVar.Offset)
					t.Stack[t.Base+valVar.Offset] = obj
				}

				if !moreMembers {
					break
				}

				// Assign to the index variable

				idxVar := stmt.KeyAndValues[0].(*ast.Ident)
				LogM(t,INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, idxVar.Name, idxVar.Offset)
				t.Stack[t.Base+idxVar.Offset] = Int(idx)

				// Execute loop body	

				breakLoop, _, returnFrom = i.ExecBlock(t, stmt.Body)

				if breakLoop || returnFrom {
					breakLoop = false
					continueLoop = false
					break
				}

				// increment the loop iteration index
				idx += 1
			}

		}

	case 0:
		if nCollections == 1 {

			// 5. for val in anyCollection  // if map, is keys	

			// now do the looping

			for {
				moreMembers := false

				obj, nextMemberFound := <-iters[0]

				if nextMemberFound {
					moreMembers = true
				}

				if !moreMembers {
					break
				}

				// Assign to the value variable

				valVar := stmt.KeyAndValues[0].(*ast.Ident)
				LogM(t,INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, valVar.Name, valVar.Offset)
				t.Stack[t.Base+valVar.Offset] = obj

				// Execute loop body	

				breakLoop, _, returnFrom = i.ExecBlock(t, stmt.Body)

				if breakLoop || returnFrom {
					breakLoop = false
					continueLoop = false
					break
				}

				// increment the loop iteration index
				idx += 1
			}
		} else { // more than one collection - they must be ordered 	

			// 6. for val1 val2 in listOfOrderedSetOrOrderedMap listOrOrderedSetOrOrderedMap  // if map, is keys

			for collPos = stackPosBefore + 1; collPos <= lastCollectionPos; collPos++ {
				collection := t.Stack[collPos].(RCollection)
				if !collection.IsOrdered() {
					rterr.Stop("Expecting lists or other ordered collections when construct is 'for val1 val2 ... in coll1 coll2 ...'.")
				}
			}

			// now do the looping

			for {
				moreMembers := false

				for k := 0; k < len(iters); k++ {
					obj, nextMemberFound := <-iters[k]

					if nextMemberFound {
						moreMembers = true
					} else if (k == len(iters)-1) && (!moreMembers) { // we are done. All iterators are exhausted.
						break
					}

					// Assign to the value variable

					valVar := stmt.KeyAndValues[k].(*ast.Ident)
					LogM(t,INTERP2_, "for range assignment base %d varname %s offset %d\n", t.Base, valVar.Name, valVar.Offset)
					t.Stack[t.Base+valVar.Offset] = obj
				}

				if !moreMembers {
					break
				}

				// Execute loop body	

				breakLoop, _, returnFrom = i.ExecBlock(t, stmt.Body)

				if breakLoop || returnFrom {
					breakLoop = false
					continueLoop = false
					break
				}

				// increment the loop iteration index
				idx += 1
			}

		}

	default:
		rterr.Stop("too many or too few variables in for statement.")
	}

	/*	




			for  {
				moreMembers := false

			    var key RObject 

				handled := false

				switch keyOffset {
				   case 2: // there must be a single ordered map collection

		              idxVar := stmt.KeyAndValues[0].(*ast.Ident)
				      Log(INTERP2_,"for range assignment base %d varname %s offset %d\n",t.Base,idxVar.Name,idxVar.Offset)
					  t.Stack[t.Base + idxVar.Offset] = Int(idx)

					  key = <-iters[0] 

		              keyVar := stmt.KeyAndValues[1].(*ast.Ident)	
				      Log(INTERP2_,"for range assignment base %d varname %s offset %d\n",t.Base,keyVar.Name,keyVar.Offset)
					  t.Stack[t.Base + keyVar.Offset] = key			

					  collPos := stackPosBefore + 1
					  mapColl := t.Stack[collPos].(Map)
				      obj,_ = mapColl.Get(key)		       // TODO Implement Get!!!!!!!!!!!!	

		              valVar := stmt.KeyAndValues[2].(*ast.Ident)	
				      Log(INTERP2_,"for range assignment base %d varname %s offset %d\n",t.Base,valVar.Name,valVar.Offset)
					  t.Stack[t.Base + valVar.Offset] = obj	

				 case 1:	 
		              idxVar := stmt.KeyAndValues[0].(*ast.Ident)

		              // TODO TODO !!!!
		              // Have to check if the first and only collection is a Map
		              // - if so, idxVar gets the key - if not, it gets the index

					  collPos := stackPosBefore + 1
					  coll := t.Stack[collPos].(RCollection)
			          if coll.IsMap() && nCollections == 1 {
				      }

				      Log(INTERP2_,"for range assignment base %d varname %s offset %d\n",t.Base,idxVar.Name,idxVar.Offset)
					  t.Stack[t.Base + idxVar.Offset] = Int(idx)

				}	

				for j:= 0; j < len(iters); j++ {
					obj, nextMemberFound := <-iters[j]
					if nextMemberFound {
						moreMembers = true
					} 

					switch keyOffset {
				       case 2: // Collection must be an OrderedMap
					      key = obj
						  mapColl := t.Stack[collPos].(Map)
					      obj,_ = mapColl.Get(key)

					      stmt.KeyAndValues[0]   // assign it Int(idx)
					      stmt.KeyAndValues[1]   // assign it key
					      stmt.KeyAndValues[2]   // assign it obj
					   case 1:
						  // Have

					   case 0:
					   default: 
					      rterr.Stop("More l-values than allowed in for range statement.")	
					}

					// Assign to the right variable



				}
				if ! moreMembers {
					break
				}

				// set index variable if there is one to idx

				// Execute loop body



				idx += 1
			}






			collection := t.Pop().(RCollection)
			if collection.IsList() {
				list := collection.(List)
				v := list.Vector()
		        len := v.Length()

		        for i := 0; i < len; i++ {
			         idx := Int(i)
			         obj := v.At(i)
		        }		
			} else if collection.IsSet() {
				set := collection.(Set)
			}	

			   case *rlist:
		          list := collection.(*rlist)
		          len := list.Length()		
		          for i := 0; i < len; i++ {
			         idx := Int(i)

		          }

			   case *rset:
			   // TODO: maps
			   default:
				  rterr.Stop("Argument to 'for var(s) in collection(s)' is not a collection.")

			}
			for i,val := range 


				i.EvalExpr(t, stmt.Cond) 
				if t.Pop().IsZero() {

				} else {
				   breakLoop,_,returnFrom = i.ExecBlock(t, stmt.Body)
				   for (! breakLoop) && (! returnFrom) {
				      i.EvalExpr(t, stmt.Cond) 		
				      if t.Pop().IsZero() {
					     break
					  }	   	
			          breakLoop,_,returnFrom = i.ExecBlock(t, stmt.Body)		    
				   }
				   breakLoop = false
				   continueLoop = false
				}
		    }



	*/
	t.PopN(nCollections) // Pop the collections off the stack. 

	return
}

/*
 */
func (i *Interpreter) ExecMethodCall(t *Thread, t2 *Thread, call *ast.MethodCall) {
	defer UnM(t,TraceM(t,INTERP_TR, "ExecMethodCall"))
	nResults := i.EvalMethodCall(t, t2, call)
	t.PopN(nResults) // Discard the results of the method call. No one wants them.
}

/*
TODO
*/
func (i *Interpreter) ExecAssignmentStatement(t *Thread, stmt *ast.AssignmentStatement) {
	defer UnM(t,TraceM(t,INTERP_TR2, "ExecAssignmentStatement"))
	
	startPos := t.Pos  // top of stack at beginning of assignment statement execution

    for _,rhsExpr := range stmt.Rhs {
 		i.EvalExpr(t, rhsExpr)   	
    }
    numRhsValues := t.Pos - startPos
    numLhsLocations := len(stmt.Lhs)


    if stmt.Tok == token.ARROW { // send to channel
       if numLhsLocations != 1 || numRhsValues != 1 {   // Channel send operator only accepts one lhs channel and one rhs expr
 		   rterr.Stop("Can only send a single value to a single channel in each invocation of '<-' operator.")  
       }

	   lhsExpr := stmt.Lhs[0]
			
	   var c *Channel
	   switch lhsExpr.(type) {
	   case *ast.Ident: // A local variable or parameter or result parameter
		   LogM(t,INTERP2_, "send to channel varname %s\n", lhsExpr.(*ast.Ident).Name)
			obj, err := t.GetVar(lhsExpr.(*ast.Ident).Offset)
			if err != nil {
				rterr.Stopf("Attempt to access the value of unassigned variable %s.",lhsExpr.(*ast.Ident).Name)
			}		
            c= obj.(*Channel)
		case *ast.SelectorExpr:
		   selector := lhsExpr.(*ast.SelectorExpr)			
		   LogM(t,INTERP2_, "send to channel attr name %s\n", selector.Sel.Name)			
	  	   i.EvalSelectorExpr(t, selector)	      
		   c = t.Pop().(*Channel)      		
       }

       val := t.Pop()
       // TODO do a runtime type-compatibility check of val's type with c.ElementType
	   c.Ch <- val	    


    } else { // assignment

	    if numLhsLocations != numRhsValues  {
	    	var errMessage string
	    	if numRhsValues == 1 { 
	    		errMessage = fmt.Sprintf("Cannot assign 1 right-hand-side value to %d left-hand-side variables/attributes.",numLhsLocations)
	    	} else if numLhsLocations == 1 {
	    		errMessage = fmt.Sprintf("Cannot assign %d right-hand-side values to 1 left-hand-side variable/attribute.",numRhsValues)    		
	    	} else {
	            errMessage = fmt.Sprintf("Cannot assign %d right-hand-side values to %d left-hand-side variables/attributes.",numRhsValues, numLhsLocations)  
	        }    	
	 		rterr.Stop(errMessage)   	
	    }

	    // Pop rhs values of the stack one by one, assigning each of them to a successive lhs location, starting with the last lhs expr and going
	    // from right to left.
	    //
	    for j := numLhsLocations - 1; j >= 0; j-- {
	       lhsExpr := stmt.Lhs[j]

			switch lhsExpr.(type) {
			case *ast.Ident: // A local variable or parameter or result parameter
				LogM(t,INTERP2_, "assignment base %d varname %s offset %d\n", t.Base, lhsExpr.(*ast.Ident).Name, lhsExpr.(*ast.Ident).Offset)

				t.Stack[t.Base+lhsExpr.(*ast.Ident).Offset] = t.Pop()

				// TODO TODO TODO
				// Will have to have reserved space for the local variables here when calling the method!!!     
				// So have to know how many locals there are in the body!!!! Store it in the RMethod!!!
				// Why is this comment here? We already do that.

			// TODO handle dot expressions and [ ] selector expressions. First lhsEvaluate lhsExpr to put a
			// cell reference onto the stack, then pop it and assign to it.
			case *ast.SelectorExpr:
				selector := lhsExpr.(*ast.SelectorExpr)
				i.EvalExpr(t, selector.X) // Evaluate the left part of the selector expression.		      
				assignee := t.Pop()       // the robject whose attribute is being assigned to.

				// To speed this up at runtime, could, during parse time, have set an attr field (tbd) of the Sel ident.
				//
				// Except... We don't even know if this name is a one-arg method or an attribute, or which setter
				// (from which type) to use. TODO TODO TODO. In this usage, in lhs of an assignment,
				// it has to be an attribute or it's an error.
				//
				attr, found := assignee.Type().GetAttribute(selector.Sel.Name)
				if !found {
					rterr.Stop(fmt.Sprintf("Attribute %s not found in type %v or supertypes.", selector.Sel.Name, assignee.Type()))
				}

				switch stmt.Tok {
				case token.ASSIGN:
	                if attr.Part.CollectionType != "" {
		                val := t.Pop()
		                if val != NIL {
			               rterr.Stop("Cannot directly assign to a multi-valued attribute, unless assigning nil to clear it.")
		                }
		                err := i.rt.ClearAttr(assignee, attr)
		                if err != nil {
						   panic(err)	
						}	
	                } else {									
						err := RT.SetAttr(assignee, attr, t.Pop(), true, t.EvalContext, false)
						if err != nil {
							if strings.Contains(err.Error()," a value of type ") {
								rterr.Stop(err)
							} 
							panic(err)
						}
				    }
				case token.ADD_ASSIGN:
					err := RT.AddToAttr(assignee, attr, t.Pop(), true, t.EvalContext, false)
					if err != nil {
						if strings.Contains(err.Error()," a value of type ") {
							rterr.Stop(err)
						} 					
						panic(err)
					}
				case token.SUB_ASSIGN:
					// TODO TODO	
					err := RT.RemoveFromAttr(assignee, attr, t.Pop(), false, true)
					if err != nil {
						panic(err)
					}
				default:
					panic("Unrecognized assignment operator")
				}

			case *ast.IndexExpr:
				indexExpr := lhsExpr.(*ast.IndexExpr)
				i.EvalExpr(t, indexExpr.X) // Evaluate the left part of the index expression.		
				i.EvalExpr(t, indexExpr.Index) // Evaluate the index of the index expression.					 
				idx := t.Pop()       // the index or map key			     
				assignee := t.Pop()       // the robject whose attribute is being assigned to. OrderedCollection or Map

				collection,isCollection := assignee.(RCollection)
				if ! isCollection {
					rterr.Stopf("Cannot [index] into a non-collection of type %v.", assignee.Type())
				}

				if collection.IsIndexSettable() {
                    coll := collection.(IndexSettable)
                    var ix int
                    switch idx.(type) {
                    case Int:
                    	ix = int(int64(idx.(Int)))
                    case Int32:
                    	ix = int(int32(idx.(Int32)))
                    case Uint:
                    	ix = int(uint64(idx.(Uint)))                    	                    
                    case Uint32:
                    	ix = int(uint32(idx.(Uint32)))
                    default:
					   rterr.Stop("Index value must be an Integer")
					}

					switch stmt.Tok {
					case token.ASSIGN:
					   // No problem
					case token.ADD_ASSIGN:
						rterr.Stop("[index] += val  is not supported yet.")
					case token.SUB_ASSIGN:
						rterr.Stop("[index] -= val  is not supported yet.")
					default:
						panic("Unrecognized assignment operator")
					}	

					coll.Set(ix,t.Pop())	

/* TODO If is an ADD_ASSIGN or SUB_ASSIGN evaluate through to get the other collection and do it !!
					switch stmt.Tok {
					case token.ASSIGN:
						err := RT.SetAttr(assignee, attr, t.Pop(), true, t.EvalContext, false)
						if err != nil {
							if strings.Contains(err.Error()," a value of type ") {
								rterr.Stop(err)
							} 
							panic(err)
						}
					case token.ADD_ASSIGN:
						err := RT.AddToAttr(assignee, attr, t.Pop(), true, t.EvalContext, false)
						if err != nil {
							if strings.Contains(err.Error()," a value of type ") {
								rterr.Stop(err)
							} 					
							panic(err)
						}
					case token.SUB_ASSIGN:
						// TODO TODO	
						err := RT.RemoveFromAttr(assignee, attr, t.Pop(), false)
						if err != nil {
							panic(err)
						}
					default:
						panic("Unrecognized assignment operator")
					}	
*/


				} else if collection.IsMap() {

					switch stmt.Tok {
					case token.ASSIGN:
					   // No problem
					case token.ADD_ASSIGN:
						rterr.Stop("[Key] += val  is not supported yet.")
					case token.SUB_ASSIGN:
						rterr.Stop("[Key] -= val  is not supported yet.")
					default:
						panic("Unrecognized assignment operator")
					}			

                    theMap := collection.(Map)
	                theMap.Put(idx, t.Pop(), t.EvalContext) 

	
/* TODO If is an ADD_ASSIGN or SUB_ASSIGN evaluate through to get the other collection and do it !!
					switch stmt.Tok {
					case token.ASSIGN:
						err := RT.SetAttr(assignee, attr, t.Pop(), true, t.EvalContext, false)
						if err != nil {
							if strings.Contains(err.Error()," a value of type ") {
								rterr.Stop(err)
							} 
							panic(err)
						}
					case token.ADD_ASSIGN:
						err := RT.AddToAttr(assignee, attr, t.Pop(), true, t.EvalContext, false)
						if err != nil {
							if strings.Contains(err.Error()," a value of type ") {
								rterr.Stop(err)
							} 					
							panic(err)
						}
					case token.SUB_ASSIGN:
						// TODO TODO	
						err := RT.RemoveFromAttr(assignee, attr, t.Pop(), false)
						if err != nil {
							panic(err)
						}
					default:
						panic("Unrecognized assignment operator")
					}	
*/

				} else {
					if collection.IsList() { // Must be a sorting list
					   rterr.Stop("Cannot set element at [index] of a sorting list.")	
					} 				
					rterr.Stopf("Can only set [index] of an index-settable ordered collection or a map; not a %v.", assignee.Type())					
				}



			

			default:
				rterr.Stop("Left-hand side expr must be variable, attribute, or indexed position in map/collection.")

			}       
	    }
    }

/*
	for j, lhsExpr := range stmt.Lhs {
		rhsExpr := stmt.Rhs[j]
		i.EvalExpr(t, rhsExpr)

		//i.EvalExpr(lhsExpr) // This is not right. What I do here depends on the type of lhsExpr and type of cell it results in.
		// It is not necessarily full evaluation that is wanted.

		switch lhsExpr.(type) {
		case *ast.Ident: // A local variable or parameter or result parameter
			Log(INTERP2_, "assignment base %d varname %s offset %d\n", t.Base, lhsExpr.(*ast.Ident).Name, lhsExpr.(*ast.Ident).Offset)

			t.Stack[t.Base+lhsExpr.(*ast.Ident).Offset] = t.Pop()

			// TODO TODO TODO
			// Will have to have reserved space for the local variables here when calling the method!!!     
			// So have to know how many locals there are in the body!!!! Store it in the RMethod!!!
			// Why is this comment here? We already do that.

		// TODO handle dot expressions and [ ] selector expressions. First lhsEvaluate lhsExpr to put a
		// cell reference onto the stack, then pop it and assign to it.
		case *ast.SelectorExpr:
			selector := lhsExpr.(*ast.SelectorExpr)
			i.EvalExpr(t, selector.X) // Evaluate the left part of the selector expression.		      
			assignee := t.Pop()       // the robject whose attribute is being assigned to.

			// To speed this up at runtime, could, during parse time, have set an attr field (tbd) of the Sel ident.
			//
			// Except... We don't even know if this name is a one-arg method or an attribute, or which setter
			// (from which type) to use. TODO TODO TODO. In this usage, in lhs of an assignment,
			// it has to be an attribute or it's an error.
			//
			attr, found := assignee.Type().GetAttribute(selector.Sel.Name)
			if !found {
				panic(fmt.Sprintf("Attribute %s not found in type %v or supertypes.", selector.Sel.Name, assignee.Type()))
			}

			switch stmt.Tok {
			case token.ASSIGN:
				err := RT.SetAttr(assignee, attr, t.Pop(), true, t.EvalContext, false)
				if err != nil {
					panic(err)
				}
			case token.ADD_ASSIGN:
				err := RT.AddToAttr(assignee, attr, t.Pop(), true, t.EvalContext, false)
				if err != nil {
					panic(err)
				}
			case token.SUB_ASSIGN:
				// TODO TODO	
				err := RT.RemoveFromAttr(assignee, attr, t.Pop(), false)
				if err != nil {
					panic(err)
				}
			default:
				panic("Unrecognized assignment operator")
			}

		default:
			rterr.Stop("I only handle simple variable or attribute assignments currently. Not indexed ones.")

		}
	}
	*/
}

/*
Executes expressions in left to right order then places them under the Base pointer on the stack, ready to be
the results of the evaluation of the method.
*/
func (i *Interpreter) ExecReturnStatement(t *Thread, stmt *ast.ReturnStatement) (returnFrom bool) {
	defer UnM(t,TraceM(t,INTERP_TR, "ExecReturnStatement", "stack top index ==>", t.Base-1))

	p0 := t.Pos
    for _, resultExpr := range stmt.Results {
	   i.EvalExpr(t, resultExpr)
    }	
	p1 := t.Pos
	n := p1 -p0
		
	if stmt.IsYield {
		
		if n != t.YieldCardinality {
			rterr.Stopf("Generator expression should yield %d values but yields %d instead.",t.YieldCardinality,n)
		}
		
		// TODO This may be a temporary implementation
        for p := p0+1; p <= p1; p++ {
			t.Objs = append(t.Objs, t.Stack[p])   
		}
		t.PopN(n)
				
	} else {
		returnFrom = true
		

		for _, resultExpr := range stmt.Results {
			i.EvalExpr(t, resultExpr)
		}

		for j := n-1; j >=0; j-- {	
			t.Stack[t.Base+j-n] = t.Pop()   
		}
    }
    return
}

/*
If parent is nil, something else must take care of initializing 
the ExecutingMethod and ExecutingPackage attributes of the new thread.
*/
func (i *Interpreter) NewThread(parent *Thread) *Thread {
	defer UnM(parent,TraceM(parent,INTERP_TR, "NewThread"))
	return newThread(DEFAULT_STACK_DEPTH, i, parent)
}

/*
If parent is nil, something else must take care of initializing 
the ExecutingMethod and ExecutingPackage attributes of the new thread.
*/
func newThread(initialStackDepth int, i *Interpreter, parent *Thread) *Thread {
	t := &Thread{Pos: -1, Base: -1, Stack: make([]RObject, initialStackDepth), EvalContext: nil}
	if parent != nil {
		t.ExecutingMethod = parent.ExecutingMethod
		t.ExecutingPackage = parent.ExecutingPackage
	}
	t.EvalContext = &methodEvaluationContext{i, t}
	return t
}

/*
Not really a thread. Actually equivalent to a goroutine.

Each routine that is called pushes its context onto the stack, thus has a stack "frame".
The first element in the stack frame is an Int32 which holds the position on the stack of the beginning of the 
previous routine stack frame, or holds -1 if this is the first routine call pushed on the stack.
The value of a local variable or a routine parameter is found by taking the variable's Offset and adding it
to the Base, then dereferencing that stack item.
*/
type Thread struct {
	Pos         int // position of the top of the stack
	Base        int // position on stack of the beginning of the currently executing routine's stack frame
	Stack       []RObject
	EvalContext *methodEvaluationContext // Basically a self-reference, but includes a reference to the interpreter

	ExecutingMethod *RMethod       // Shortcut for dispatch efficiency
	ExecutingPackage *RPackage   // Shortcut for dispatch efficiency

    // This may be temporary - it is used for generators inside collection constructors, but may
    // be replaced by proper go-routine-and-channel generators or closures.
    Objs       []RObject   // A list of objects that will be built up then become owned by a proper collection object 
                           // and detached from the Thread.
	YieldCardinality int   // How many objects per iteration the current generator is expected to append to Objs
}

func (t *Thread) Push(obj RObject) {
	defer UnM(t, TraceM(t, INTERP_TR3, "Push", obj))
	t.Pos++
	if len(t.Stack) <= t.Pos {
		oldStack := t.Stack
		t.Stack = make([]RObject, len(t.Stack)*2)
		copy(t.Stack, oldStack)
	}
	t.Stack[t.Pos] = obj
	if Logging(STACK_) && Logging(INTERP_TR3) {
		t.Dump()
	}
}

func (t *Thread) Reserve(n int) {
	defer UnM(t, TraceM(t, INTERP_TR3, "Reserve", n))
	for len(t.Stack) <= t.Pos+n {
		oldStack := t.Stack
		t.Stack = make([]RObject, len(t.Stack)*2)
		copy(t.Stack, oldStack)
	}
	t.Pos += n
}

/*
Call this at the beginning of a method call, to push the previous (outer) method's stack frame base onto the stack.
When finished evaluating arguments to the call, then complete the context store by calling setBase with the
value that has been returned by PushBase().
The numReturnArgs argument says how many stack positions to leave free below the base-pointer position for
results (return values) of the method call.

Returns the position of the base pointer which holds the stack-pointer to the previous stack-frame base position.
*/
func (t *Thread) PushBase(numReturnArgs int) int {
	defer UnM(t, TraceM(t, INTERP_TR3, "PushBase"))
	if numReturnArgs > 0 {
		t.Reserve(numReturnArgs)
	}
	t.Push(Int32(t.Base))
	t.Reserve(2) // Reserve space for the currently-executing-method reference and code offset in current method
	return t.Pos - 2
}

func (t *Thread) SetBase(newBase int) {
	defer UnM(t, TraceM(t, INTERP_TR3, "SetBase", newBase))
	t.Base = newBase
}

/*
Call this to return from a method call.
Pops the stack to just before the beginning of the current routine's stack frame, and
sets the thread's Base pointer to the beginning of the previous (outer) routine's stack frame.


                            B       P
0   1   2   3   4   5   6   7   8   9
-1  v1  v2  0   v1  v2  v3  3   v1  v2

*/
func (t *Thread) PopBase() {
	defer UnM(t, TraceM(t, INTERP_TR3, "PopBase"))
	obj := t.PopN(t.Pos - t.Base + 1) // 9 - 7 + 1 = 3
	t.Base = int(obj.(Int32))
	t.ExecutingMethod = t.Stack[t.Base+1].(*RMethod)
	t.ExecutingPackage = t.ExecutingMethod.Pkg
	LogM(t, INTERP3_, "---Base = %d\n", t.Base)
}

/*
Return the value of the local variable (or parameter) with the given offset from the current routine's 
stack base.
*/
func (t *Thread) GetVar(offset int) (obj RObject, err error) {
	defer UnM(t, TraceM(t, INTERP_TR3, "GetVar", "offset", offset, "stack index", t.Base+offset))
	if offset == -99 {
		err = errors.New("Unassigned variable.")
		return
	}
	obj = t.Stack[t.Base+offset]
	return
}

func (t *Thread) Pop() RObject {
	obj := t.Stack[t.Pos]
	defer UnM(t, TraceM(t, INTERP_TR3, "Pop", "==>", obj))
	t.Stack[t.Pos] = nil // ensure var/param value is garbage-collectable if not otherwise referred to.
	t.Pos--
	if Logging(STACK_) && Logging(INTERP_TR3) {
		t.Dump()
	}
	return obj
}

/*
Efficiently pop n items off the stack.
*/
func (t *Thread) PopN(n int) RObject {
	lastPopped := t.Pos - n + 1
	obj := t.Stack[lastPopped]
	defer UnM(t, TraceM(t, INTERP_TR3, "PopN", n, "==>", obj))
	for i := t.Pos; i >= lastPopped; i-- {
		t.Stack[i] = nil // ensure var/param value is garbage-collectable if not otherwise referred to.
	}
	t.Pos -= n
	if Logging(STACK_) && Logging(INTERP_TR3) {
		t.Dump()
	}
	return obj
}

/*
Return a slice which represents the top n objects on the thread's stack.
In order from bottom most (oldest pushed) to top most (most recently pushed).
*/
func (t *Thread) TopN(n int) []RObject {
	return t.Stack[t.Pos-n+1 : t.Pos+1]
}

func (t *Thread) copyStackFrameFrom(parent *Thread, numReturnVals int) {
   n := parent.Pos - parent.Base + numReturnVals + 1	
   src := parent.Stack[parent.Base - numReturnVals:parent.Pos+1]
   if copy(t.Stack, src) != n {
   	   panic("stack copy range exception during go-routine spawn.")
   }
   t.Base = numReturnVals
   t.Pos = n - 1

}

/*
DEBUG printout of stack
*/
func (t *Thread) Dump() {
	LogMutex.Lock()
	fmt.Println("------STACK----------")
	for i := t.Pos; i >= 0; i-- {
		fmt.Printf("%3d: %v\n", i, t.Stack[i])
	}
	fmt.Printf("Pos : %d\n", t.Pos)
	fmt.Printf("Base : %d\n", t.Base)
	fmt.Println("---------------------")
	LogMutex.Unlock()
}
